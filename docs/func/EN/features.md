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

# Functional Overview

This document describes the main functional areas of the system. A short
overview is in the root `README.en.md`.

---

## Jobs and modes

A job is the unit of backup configuration; it lives in `lentovodec.toml`
under the `[[jobs]]` section:

| Field | Type | Description |
|---|---|---|
| `Name` | string | Unique job name; the key in the config |
| `Description` | string | Human-readable description |
| `Mode` | string | `append` or `mirror` |
| `Paths` | []string | Backup roots (files or directories) |
| `Exclude` | []string | Glob exclude patterns |
| `span_depth` | int | Directory nesting depth for grouping spanning parts; 0 (default) — split by files |

Modes:

| Mode | Semantics |
|---|---|
| `append` | New and modified files are backed up; deletions are not tracked. Each session adds new file versions. |
| `mirror` | In addition to `append`, tombstones (deletion records) are recorded. A restore can reconstruct the directory mirror **as of any session**. |

### Exclude patterns

- `path.Match` patterns (a single segment) and `doublestar`-style `/**/`
  for recursive matches are supported.
- Examples: `*.tmp`, `node_modules`, `.git/**`, `**/.DS_Store`.
- A pattern without `/` matches the base name — `node_modules` prunes
  such directories at any depth.
- Matching is done against the full normalized path and is
  case-sensitive.
- A match on an ancestor directory prunes the whole subtree.

Job management — `lentovodec jobs list/add/remove` (see
[cli.md](cli.md)); in the Web UI — the Jobs screen (editing is
implemented as remove+add).

### Path browser in the Web UI

The job and restore forms in the Web UI have a "Browse" button. It opens
a server-side file browser with breadcrumbs, an up-navigation link, a
local search box, and a hidden-entries toggle. In the job form you can
select multiple roots; in the `restore --dest` form — a single
directory. Manual path entry remains available. The browser is
read-only: it does not create, delete, or rename files.

---

## Sessions and incrementality

A session is one backup run written to the cartridge. A cartridge holds
several sessions numbered from 1; the first session on a cartridge is
always FULL.

- File states are computed by comparing the current filesystem snapshot
  with the snapshot of the last session in the catalog:
  - `Added` — the file exists on the FS and was not present in the
    previous session;
  - `Modified` — size or mtime changed (xxhash64 is computed only for
    files that made it into the session);
  - `Deleted` — was in the previous session, now gone (mirror only).
- **FULL** (`--full` or the first session): rewind to the beginning,
  rewrite the chain starting at session 1; old sessions are purged from
  the catalog. Recommended when starting a new increment chain.
- **INC** (default): append at end-of-data (EOD).
- `--dry-run` — scanning and statistics only, nothing written to the
  tape.
- `--verify` — verify-after-write: freshly written sessions are read
  back and checked against the index by xxhash. Why: LTO ECC protects
  bits, not content — overwriting of adjacent tracks and servo data
  errors are only caught by re-reading. The cost is roughly ×2 write
  time; only the sessions of the current run are checked (the whole
  cartridge is `tape readtest`, see "Tape and cartridges"); with
  spanning, each part is verified on its own cartridge before the
  change. On failure the session is **not** rolled back: the data is
  already on tape and in the catalog, and rewriting does not cure the
  medium — the command reports "session N was written but does not read
  back"; the decision (cartridge repair/replacement, a re-backup) is
  the operator's. In the daemon it is the same — the `verify=true`
  parameter of a backup start (the `verify` phase in task progress).
- On completion, statistics are printed: scanned / added / modified /
  deleted / bytes to write.
- If a file changed between the scan and the write, the encoder checks
  the hash and fails instead of silently writing an inconsistent index.

---

## Restore

Three modes (see [cli.md](cli.md) for the CLI syntax):

| Mode | When to use | Mechanics |
|---|---|---|
| **Full** | Disaster recovery of the whole cartridge | Sessions are read sequentially from the beginning; for each one — the index, then the tar with xxhash checks. Damaged sessions are skipped, moving on to the next. |
| **Selective** | Selected files from a specific session | Positioning to the session by number, selecting files by paths and subtrees. Available in the Web UI (session file browser). |
| **Smart** | Restore by path without knowing the session | The catalog returns all copies of the path from newest to oldest; on a hash mismatch — fallback to the next copy. No healthy copy left → `NoHealthyCopyError`. |

Common properties:

- xxhash64 is verified on every restore; a file counts as read only
  when its hash matches.
- `--dest` — restore into a safe directory (index paths are relocated
  under it, volume names like `C:` are stripped); `--original` — to the
  original paths from the index (in the Web UI — with a confirmation
  dialog about overwriting).
- In mirror chains, tombstones of the sessions read remove the
  corresponding files from the target directory — this is how the
  actual state of the tree is reconstructed.

---

## Catalog

The catalog is an SQLite database (`db` from the config, WAL mode) with
metadata for all cartridges, sessions, and files:

- cartridges: UUID, name, format time;
- sessions: number on the cartridge, type (FULL/INC), time, run
  identifier;
- files: path, size, mtime, directory flag, xxhash64, state
  (`A`/`M`/`D`).

Operations (CLI `catalog …`, REST `/api/catalog/…`, Web UI):

- listings of cartridges and sessions (with a cartridge filter), session
  files;
- substring search over paths;
- deleting a session from the catalog: **the data on the tape remains**,
  but the path to it is lost;
- prune — deleting all sessions older than N days;
- rebuild (CLI, local) — catalog reconstruction from the inserted
  cartridge: the label and the JSON indexes of all its sessions are
  transferred into the database. The DR scenario is "lost or corrupted
  `lentovodec.db`", a cartridge unknown to the catalog, or migration to
  a new machine. The tape is the source of truth, the catalog is a
  derivative: **rebuild ≠ verification** — the tar stream and hashes
  are not checked (that is the job of `tape readtest`); this is why
  rebuild over the indexes is fast. Idempotent: existing sessions are
  skipped, re-running and running against a live catalog are safe. A
  cartridge with a chain continuation ends with a hint to insert the
  next one and repeat — the data of the read part is already saved.

The catalog survives restarts (it is stored on disk) and is the data
source for smart restore. Physical corruption of a single file copy on
the tape is not fatal: smart restore will take an older healthy copy.

---

## Tape and cartridges

- **Formatting** (`tape format <name> [--force]`): rewind, write the
  JSON label (UUID, name, time), a double filemark = the EOD of a blank
  cartridge, register in the catalog. Re-formatting a used cartridge
  requires `--force`.
- **Diagnostics**: `tape readtest` reads the whole cartridge verifying
  hashes without writing to the FS (a medium check); `tape info` shows
  the label and the active TapeAlert flags of the drive (for example, a
  cleaning request, limited media life, or read/write errors). If the
  drive does not support SG_IO, the label is still returned.
- **Ejection**: `tape eject` (MTOFFL).
- Device selection — the global flag `--device` or the `device` key in
  TOML. A character device (`/dev/nst0`) is a real drive; a regular
  file is the `filetape` emulator (a non-existent path outside `/dev`
  is created as a new file tape).
- Legacy `nil-backup` cartridges (`NIL_BACKUP_TAPE`) are deliberately
  not read: `ErrForeignFormat` naming the magic string. Old data is only
  readable by the old binary, or re-format consciously with `--force`.

### Cartridge capacity and multi-volume backups (spanning)

The `capacity` (estimated cartridge capacity, `"2.2T"` with a margin
for LTO-6) and `min_tail` (remaining-space threshold; default — 5% of
capacity) keys in TOML enable the part planner:

- **An honest error before writing**: a single file larger than the
  cartridge capacity (or its remaining space when appending) fails
  right after scanning, before the tape is touched; files are never cut
  into pieces.
- **Remaining space when appending**: if less than `min_tail` is left on
  the cartridge, the new session starts on a new cartridge instead of
  cramming in a couple of gigabytes.
- **Multi-volume backup**: a session that does not fit on one cartridge
  is written as parts onto several cartridges. Each part is a
  self-contained complete session (readable with plain GNU tar); the
  cartridges are linked by continuation pointers. In terminal mode the
  tape change is an interactive prompt (Enter accepts the suggested
  name); for scripts there is the `--next-tape` flag (see
  [cli.md](cli.md)). The parts of one run are visible in the catalog
  under a common run identifier.
- **Filling up mid-part**: the cartridge is closed with a continuation
  pointer, the part is moved wholesale to the next cartridge, and the
  filled one stays intact for future appends. Two parts in a row that
  do not fit onto a whole cartridge — an error suggesting you decrease
  `capacity`.
- **Directory-based splitting (`span_depth`)**: the per-job
  `span_depth` key (int, default 0) makes the planner cut parts along
  Nth-level directory boundaries under the job root instead of by
  files: "does not fit — the whole group rolls back to the start of the
  next directory". The benefit is locality: a subtree lives on one
  cartridge, a selective restore of the subtree touches fewer
  cartridges, and cartridge contents are intuitive ("movies on
  LTO-001"). A group larger than a whole cartridge is split by files
  inside it (a fallback — otherwise such a directory would never be
  backed up); average cartridge fill is slightly worse than with
  file-based splitting — a deliberate trade-off.

Following the cartridge chain during restore and verification works in
terminal mode (see below) and in the daemon (a task pausing for a tape
change — see "Daemon and background tasks").

### Restoring across a cartridge chain

- **Full restore** (`restore` without `--paths`) follows continuation
  pointers: having read a cartridge with a continuation, Lentovodets
  asks "Cartridge X read. Insert cartridge Y (part N) and press Enter:"
  and continues from session 1 of the next cartridge. Parts are written
  into the same destination directory in order.
- **Protection against the "wrong" cartridge**: the inserted cartridge
  is verified by its label name and the back-reference of its first
  session to the previous cartridge; a mismatch is a
  `ChainMismatchError` raised before any data is restored. Insert the
  right cartridge and re-run — the restore will continue from where it
  stopped.
- **No catalog needed**: the chain is read from the tapes themselves
  (a DR scenario).
- **Without a terminal** (cron, scripts): the restore ends with a
  warning at the cartridge boundary — insert the next cartridge and
  re-run the command; the files of the read part are already restored.
- **Chain verification**: `tape readtest` follows the same logic and
  prints a report for every cartridge (sessions/files/bytes).
- **Selective restore** of a part's session requires that part's
  cartridge in the drive; on a miss the error prompts which cartridge
  to insert.

The on-tape data format — [tape-format.md](tape-format.md).

---

## Daemon and background tasks

`lentovodec daemon` is a long-running process with a REST API, a Web
UI, and a background task registry.

- **One active task**: there is a single drive, so parallel backups or
  restores are impossible — an attempt fails with a "busy" error; a
  task awaiting a cartridge (`awaiting_tape`) also occupies the drive.
- **In-memory task registry**: restarting the daemon loses the registry
  (but not the data — completed sessions are already on tape and in the
  catalog); an active task gets up to 30 seconds to finish on shutdown.
- **Progress**: phase (`scan`/`write`/`finalize`), current file,
  processed/total bytes, percent, speed (exponential smoothing), the
  last log lines. The CLI/Web UI polls
  `GET /api/tasks/{id}/progress` once per second (no WebSocket/SSE).
- **Tape-change pause (spanning)**: when a backup or restore needs the
  next cartridge of the chain, the task enters the `awaiting_tape`
  state and waits for the operator: the Web UI shows a dialog with the
  message and the suggested cartridge name; scripts can continue on
  their own — `POST /api/tasks/{id}/continue` with the body
  `{"tape_name": "..."}` (an empty name accepts the suggestion;
  `X-API-Key` works too). A new backup cartridge is formatted and
  registered automatically; a restore cartridge is verified by label
  and chain back-reference. There is no separate cancel endpoint: an
  awaiting task is cancelled by stopping the daemon — the wait closes
  immediately, the task ends with a cancellation error, and the parts
  already fixed on the tapes remain intact (the catalog rolls back the
  unfinished run).
- **Device**: the path is changed via `POST /api/settings` (409 while a
  task is active); the tape is opened for the duration of an operation,
  and between operations the drive is free.
- **Graceful shutdown**: SIGINT/SIGTERM → HTTP shutdown (5 s) → waiting
  for the active task (up to 30 s).

---

## Web UI

Five screens (the Vue 3 bundle is embedded in the binary and served by
the daemon):

| Screen | Capabilities |
|---|---|
| **Login** | Login/password form; the session token is kept in `localStorage`; shown instead of the whole UI on a 401. |
| **Tape** | Current cartridge info, the format form (with `force`), eject, device path editor. |
| **Jobs** | Job cards with description and mode; start (full/inc); create/edit form; progress panel (% bar, speed, current file, log), and in the `awaiting_tape` state — the continuation dialog with the tape change. |
| **Catalog** | Session table with a cartridge filter; deletion. |
| **Files** | File browser of the selected session: breadcrumbs, checkboxes, directory selection (expanded into files), restore into a safe folder or to original paths. Tombstones (`D`) are not restorable — they have no checkboxes. |

Russian and English languages (a switch, the choice kept in
`localStorage`).
Dev mode — `make web-dev` (Vite on `:5173`, proxying `/api` to the
daemon).

---

## Security and network access

### Daemon authentication

| Scenario | Method | Who |
|---|---|---|
| Browser / CLI daemon commands | `Authorization: Bearer <session-id>` | A user logged in via `POST /api/auth/login` |
| Scripts, curl, monitoring | `X-API-Key: <api_key from TOML>` | Programmatic access |

- The password is stored as a bcrypt hash (`lentovodec passwd` generates
  the hash for TOML); the username is compared in constant time.
- A session is 256 random bits from `crypto/rand`, living in the
  daemon's memory with a TTL (`session_ttl`, 72h by default) and a
  limit of 1024; no JWT and no signatures — revoking everything =
  restarting the daemon.
- Rate limit on `/api/auth/login`: 5 attempts / 30 s per IP, then 429;
  the counter resets on a successful login.
- Audit log: `login_success` / `login_failure` (IP, name) go into the
  common slog log with the `event=auth` field; task creation and tape
  formatting are logged with the session user's name.

### Network access

- By default `bind = 127.0.0.1:29201` — the daemon is not visible from
  the network; remote management goes through an SSH tunnel (encryption
  and authentication come from SSH).
- LAN access is a deliberate operator choice: a non-loopback `bind`
  without a configured password — a startup failure with a clear error.
- HTTP without TLS is an accepted homelab compromise (the token travels
  over your own cable/WPA2); for TLS — a reverse proxy or an SSH
  tunnel.

### Honest model boundaries

- One user, no roles.
- Sessions do not survive a daemon restart (you will have to log in
  again).
- No WebSocket/SSE — progress is polling-only.

The rootless drive access model and deployment — in
[os-setup.md](os-setup.md).

## Special files

Symbolic links, including dangling symlinks, and hard links are
supported: on restore, the link and the shared inode are preserved
respectively. FIFOs, sockets, character/block devices, xattrs, ACLs,
and filesystem flags are not transferred; such entries are skipped by
the scanner and counted in `SkippedSpecials`.
