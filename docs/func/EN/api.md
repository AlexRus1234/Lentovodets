<!--
Lentovodets — tape backup system (LTO archiver with catalog and Web UI)
Copyright (C) 2026 AlexRus1234

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
-->

# Daemon REST API

The base path is `/api`; the format is JSON. Errors look like
`{"error": "...", "code": "..."}`. The REST API is the daemon's only
interface: the Web UI and the CLI daemon commands are built on top of
it.

---

## Authentication

All endpoints except `GET /api/status` and `POST /api/auth/login`
require one of two methods:

| Scenario | Method | Who |
|---|---|---|
| Web UI, CLI daemon commands | `Authorization: Bearer <session-id>` | A user logged in via `/api/auth/login` |
| Scripts, curl, monitoring | `X-API-Key: <api_key from TOML>` | Programmatic access |

- An unauthenticated request → `401 {"error": "unauthorized", "code":
  "auth_required"}`.
- A session is 256 random bits from `crypto/rand`, stored in the
  daemon's memory (an in-memory map with a TTL of `session_ttl` from
  the config, 72h by default, limit 1024). JWT and signatures are not
  used; revoking all sessions is done by restarting the daemon.
- The API key is compared in constant time (`crypto/subtle`); an empty
  `api_key` in TOML means the key is disabled.
- Header-based authentication was chosen over cookies deliberately:
  CSRF is excluded (a third-party page cannot set a custom
  header).
- **Rate limit** on `/api/auth/login`: 5 attempts / 30 s per IP, above
  that — `429`; the counter resets on a successful login.
- Login events (success/failure, IP, name) go into the audit log
  (`slog`, field `event=auth`); task creation/deletion and tape
  formatting are logged with the session user's name.

---

## Endpoints

### Authentication

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/auth/login` | `{username, password}` → `{token, expires_at}`; rate limit 5/30 s per IP |
| `POST` | `/api/auth/logout` | Delete the current session |

### Status and config

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Healthcheck: version, whether a tape is present (probe open+close). A device occupied by a background task or a tape operation counts as available — it is opened by its owner |
| `GET` | `/api/config` | Current config (TOML as text) |
| `GET` | `/api/settings` | Current device settings |
| `POST` | `/api/settings` | Update the `device` path; 409 while a task is active |

### File system

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/fs/list?path=/abs` | Listing of the immediate contents of a server filesystem directory |

The response looks like:

```json
{
  "path": "/tank/data",
  "parent": "/tank",
  "entries": [{"name": "media", "is_dir": true, "size": 0, "mtime": 0}]
}
```

The path is read on the daemon's behalf and protected by the same
Bearer/API-key authentication as the other API methods: access to the
tree equals administrator access. Directories come before files;
hidden entries are not filtered out. 404 `not_found` means a missing
path, 403 `forbidden` — missing permissions, 400 `bad_request` — a
path to a file instead of a directory.

### Tape

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/tape/info` | Read the `TapeLabel` of the current cartridge and the active TapeAlert flags of the drive |
| `POST` | `/api/tape/eject` | Eject the cartridge |
| `POST` | `/api/tape/format?name=&force=` | Format the cartridge |

Drive access is serialized (the st driver allows a single open
descriptor): during a background backup/restore task, tape operations
immediately return `409 {"code": "task_running"}`, without waiting for
the device to be released. A drive without a cartridge (ENOMEDIUM) —
`409 {"error":
"no cartridge in the drive", "code": "no_medium"}` instead of a raw
errno.

The `alerts` field contains `{name, code, critical}` objects. When the
diagnostics are unavailable, an empty array is returned while the
label stays available.

### Jobs

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/jobs` | List jobs from TOML |
| `POST` | `/api/jobs` | Add a job to TOML |
| `DELETE` | `/api/jobs/{name}` | Delete a job from TOML |

### Background tasks

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/backup/start?job=&full=&verify=` | Start a backup, return a `taskID`; `verify=true` — read the written data back with hash verification (the `verify` phase in progress) |
| `POST` | `/api/restore/start?paths=&session_id=&dest=&original=` | Start a restore; `session_id` selects a specific session |
| `GET` | `/api/tasks/active` | List active tasks (including those awaiting a cartridge) |
| `GET` | `/api/tasks/{id}/progress` | Task progress (the object below) |
| `POST` | `/api/tasks/{id}/continue` | Continue a task in `awaiting_tape`: body `{"tape_name": "..."}` (empty/missing — the suggested name); 409 — the task is not awaiting, 404 — no such task. The event goes into the audit log (`event=task_continue`, the session user or `api-key`) |

Only **one** task is active at a time (there is a single drive):
starting another before the previous one finishes fails with a "busy"
error. A task awaiting a cartridge (`awaiting_tape`) counts as active.
The registry is in-memory: restarting the daemon loses the task
registry, but not the data.

The progress object:

```json
{
  "id": "task-abcdef12",
  "state": "running",
  "phase": "write",
  "current_file": "/path/to/file",
  "processed_bytes": 1234567,
  "total_bytes": 9876543,
  "percent": 12.5,
  "speed_mbps": 145.2,
  "logs": ["...last 50 lines..."],
  "error": "",
  "message": "",
  "suggested_tape_name": ""
}
```

`state` — `running | awaiting_tape | success | error`; `phase` —
`scan | write | verify | finalize`; `logs` — a ring buffer of the
task's last log lines.

`awaiting_tape` is the spanning extension: the task is paused and needs
the next cartridge of the chain. `message` carries the text for the
operator (for example, "cartridge test-tape closed (span): insert a
blank cartridge media-002 (part 2)"), and `suggested_tape_name` — the
name pre-filled into the dialog (an empty `tape_name` in continue
accepts it). After a successful continue the task returns to
`running`. An awaiting task is cancelled by stopping the daemon (there
is no cancel endpoint).

### Catalog

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/catalog/tapes` | List cartridges |
| `GET` | `/api/catalog/sessions?tape=` | List sessions (filter by cartridge UUID); the session object includes `part` — the part number in a spanning run (a regular session — `1`) |
| `GET` | `/api/catalog/sessions/{id}/files` | Files of a session |
| `GET` | `/api/catalog/search?q=` | Substring file search |
| `GET` | `/api/catalog/file-copies?path=` | All copies of an exact path, newest first |
| `DELETE` | `/api/catalog/sessions/{id}` | Delete a session from the catalog |
| `POST` | `/api/catalog/prune?days=` | Delete sessions older than N days |

The `file-copies` response looks like:

```json
{"path":"/tank/data/a.txt","copies":[{"path":"/tank/data/a.txt","session_id":7,"session_num":2,"tape_uuid":"uuid","tape_name":"LTO-002","timestamp":1700000000,"size":12,"mod_time":0,"hash":"0123456789abcdef","state":"M","is_dir":false}]}
```

An empty `path` yields `400 bad_request`; an unknown path returns `200`
with an empty `copies`. The request requires authentication.

For `POST /api/restore/start`, the `session_id` parameter enables
selective restore from that session. If `session_id` is given without
`paths`, all entries of the selected session are restored; with
`paths`, only the given paths and their subtrees are written to disk
(the remaining session entries are read and hash-checked but not
extracted). With `dest`, `..` paths in the tape index are rejected and
writing through restored symlinks is forbidden — the restore never
leaves the destination directory.

### Static assets

`GET /` and everything not matching `/api/*` is served from the
embedded `embed.FS` with the built Vue bundle (SPA fallback,
content-type by extension).

---

## Examples

### Login and starting a backup

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:29201/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret"}' | jq -r .token)

TASK=$(curl -s -X POST \
  "http://127.0.0.1:29201/api/backup/start?job=media" \
  -H "Authorization: Bearer $TOKEN" | jq -r .task_id)

curl -s http://127.0.0.1:29201/api/tasks/$TASK/progress \
  -H "Authorization: Bearer $TOKEN" | jq
```

### The same via `X-API-Key` (for scripts)

```bash
curl -s http://127.0.0.1:29201/api/catalog/tapes \
  -H "X-API-Key: $LENTOVODEC_API_KEY"
```

### Changing the device

```bash
curl -s -X POST http://127.0.0.1:29201/api/settings \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"device": "/dev/nst1"}'
```

### Continuing a task after a tape change (spanning)

```bash
# Progress shows state=awaiting_tape, message and suggested_tape_name
curl -s -X POST http://127.0.0.1:29201/api/tasks/$TASK/continue \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"tape_name": "media-002"}'   # or {} — accepts the suggested name
```

---

Backup/restore semantics — [features.md](features.md); the CLI —
[cli.md](cli.md); deployment and network access —
[os-setup.md](os-setup.md).
