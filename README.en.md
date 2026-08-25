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

**English** | [Русский](README.md) |

<div align="center">

<h1>Lentovodets</h1>

**Backup system for LTO tape drives**

Lentovodets (Russian for "tape guide") is a self-contained backup system
for LTO tape drives. The CLI, the HTTP daemon with a Web UI, and the tape
codec are combined into a single executable; metadata for all cartridges,
sessions, and files is stored in a local SQLite catalog. No external
database or container infrastructure is required, and no root privileges
are needed.

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue.svg?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg?style=flat-square)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D.svg?style=flat-square)](https://vuejs.org/)
[![Vite](https://img.shields.io/badge/Vite-7-646CFF.svg?style=flat-square)](https://vite.dev/)
[![Platform](https://img.shields.io/badge/Linux-any-1793D1.svg?style=flat-square)](#quick-start)

</div>

> **Cartridges are not tied to Lentovodets.** Every cartridge is a
> plain tar archive: on any Unix machine with an LTO drive it is read
> by bare GNU tar, without Lentovodets and its catalog:
>
> ```bash
> mt -f /dev/nst0 rewind && mt -f /dev/nst0 fsf 2   # past the label and session index
> dd if=/dev/nst0 bs=256k | tar -x                  # the cartridge data
> ```
>
> Lentovodets merely adds a catalog, incremental sessions, multi-volume
> chains, and xxhash64 verification on top (the DR recipe —
> [`docs/func/EN/tape-format.md`](docs/func/EN/tape-format.md)).

---

## Contents

- [Features](#features)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Access and deployment](#access-and-deployment)
- [CLI, API, and Web UI](#cli-api-and-web-ui)
- [Project structure](#project-structure)
- [Technology stack](#technology-stack)
- [Development](#development)
- [License](#license)

> Extended documentation on features, CLI, REST API, tape format, and
> deployment is available in [`docs/func/EN/`](docs/func/EN/README.md).
> This file provides a system overview and initial setup instructions.

The project was developed according to a predefined architecture; an AI
assistant was used while preparing the source code.[^1]

---

## Features

### Backup

- Incremental backups: new and modified files are detected by size,
  mtime, and xxhash64; symbolic and hard links are preserved, special
  files (FIFOs, sockets, devices) are skipped and counted
- Multi-volume backups with cartridge continuation (spanning): a session
  is split at file boundaries, each cartridge can be read with plain GNU
  tar (DR recipe — `docs/func/EN/tape-format.md`); per-job `span_depth`
  splits at directory boundaries — a whole subtree stays on one
  cartridge; tape changes are an interactive CLI prompt or a daemon
  dialog (`awaiting_tape`)
- Job modes `append` (versioned appends) and `mirror` (deletion
  tombstones — a restore can reconstruct the directory mirror as of any
  session)
- Full (`--full`) and incremental sessions; the first session on a
  cartridge is always FULL; `--dry-run` for a trial run
- Verify-after-write (`--verify`): freshly written sessions are read
  back immediately and every file's xxhash is checked against the index
  (~×2 write time, off by default; full media diagnostics — `readtest`)
- Exclude filters: glob and doublestar (`**/.DS_Store`, `.git/**`,
  `node_modules`)
- Session statistics (scanned/added/modified/deleted/bytes) and progress
  in real time (current file, %, speed)

### Restore

- **Full** — the whole cartridge sequentially, including following a
  spanning chain (tape change with label and chain back-reference
  checks); damaged sessions are skipped
- **Selective** — selected paths from a specific session (Web UI); for a
  chain-part session it prompts which cartridge to insert
- **Smart** — by paths through the catalog: the freshest copy is taken,
  a corrupt block falls back to older copies; no healthy copy left → a
  clear error
- xxhash64 is verified on every restore
- Restore into a safe directory (`--dest`) or to original paths
  (`--original`); `--dest` never writes outside the destination
  directory (`..` paths and escapes through restored symlinks are
  rejected)

### Catalog

- SQLite (WAL): cartridges, sessions, file copies of all sessions
- Substring search, per-cartridge filtering, session deletion, pruning
  of stale sessions
- File version history: all copies of a path with date, cartridge,
  session, and hash (`catalog copies`, Web UI) — any version can be
  restored selectively
- `catalog rebuild` — catalog reconstruction from the JSON indexes of
  the inserted cartridge (DR: lost `lentovodec.db`, a cartridge unknown
  to the catalog, migration to a new machine); idempotent, the tar
  stream is not read
- Smart restore relies on the catalog and survives physical corruption
  of an individual file copy

### Tape

- Own format `LENTOVODEC_TAPE_V2`: JSON cartridge label, sessions of
  "JSON index + tar stream" with filemark invariants (details —
  [`docs/func/EN/tape-format.md`](docs/func/EN/tape-format.md))
- `format` (UUID and cartridge name), `readtest` (diagnostic read with
  hash verification, nothing written to the FS, a report per cartridge
  of the chain), `eject`, `info` (label + active TapeAlert flags of the
  drive: cleaning required, media wear, read/write errors)
- The `filetape` emulator: a regular file behaves like a tape — development
  and CI on any OS without a drive

### Interfaces and security

- CLI: local commands access the tape directly, daemon commands go
  through the HTTP API
- Daemon: background backup/restore tasks (one active at a time — there
  is a single drive), REST API, graceful shutdown; a task at a cartridge
  boundary enters `awaiting_tape` and is continued from the Web UI or
  `POST /api/tasks/{id}/continue`; webhook notifications about task
  completion (`webhook_url`)
- Web UI (Vue 3, embedded into the binary): tape, jobs, catalog, files,
  restore; a server-side file browser (choosing job roots and the
  restore destination), file version history; Russian and English
- Authentication: login/password (bcrypt) + in-memory Bearer sessions,
  `X-API-Key` for scripts, rate limit on login
- Rootless: the drive is managed without root and without
  `CAP_SYS_RAWIO` (group `tape`); by default the daemon listens on
  loopback only

A detailed description is in [`docs/func/EN/features.md`](docs/func/EN/features.md).

---

## Quick start

### Requirements

| Component | Version | Purpose |
|---|---|---|
| **Go** | 1.26+ | Building the binary |
| **Node.js** | 22+ | Building the Web UI |
| **Linux + LTO drive** | `/dev/nst*` | Production environment |
| Windows / macOS | — | Development without a drive (`filetape` emulator) |

### Build

```bash
# Build the Web UI and the server part in the order used by CI:
make web-build
make build        # binary bin/lentovodec (without the drive driver)
make build-tape   # same + adapter/linuxtape (build tag `tape`, Linux)
```

CGO is not required (SQLite is `modernc.org/sqlite`), the binary is static.

Release build (as in CI):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -tags tape -trimpath -ldflags="-s -w \
  -X lentovodec/cmd/lentovodec.Version=1.0.0" \
  -o lentovodec ./cmd/lentovodec
```

> Release binaries (`lentovodets-<version>-linux-amd64` + `.sha256`) are
> published manually from CI to Forgejo Packages and Releases; for
> development, build from source.

### First run

```bash
# 1. A backup job (written into lentovodec.toml):
lentovodec jobs add media --paths /tank/data/media --mode mirror \
    --exclude "**/.DS_Store,**/*.partial"

# 2. The cartridge. The device is set with --device (default /dev/nst0);
#    a regular file works as a tape emulator (dev/test):
lentovodec --device /tmp/tape.img tape format LTO-001
lentovodec --device /tmp/tape.img backup media

# 3. Restore:
lentovodec --device /tmp/tape.img restore \
    --paths /tank/data/media/movie.mkv --dest /safe

# 4. Daemon: REST API + Web UI at http://127.0.0.1:29201
lentovodec passwd     # bcrypt hash → web_password_hash in TOML
lentovodec daemon
```

---

## Configuration

`lentovodec.toml` (in the working directory by default; global flag
`--config`):

```toml
db    = "lentovodec.db"
device = "/dev/nst0"
log   = "lentovodec.log"
server = "http://127.0.0.1:29201"
log_level = "info"           # debug | info | warn | error

# --- Web access (daemon) ---
bind = "127.0.0.1:29201"     # loopback = not visible from the network
                              # (SSH tunnel); a LAN address requires a
                              # configured password
web_username = "admin"       # exactly one account; empty string = auth off
                              # (allowed ONLY when bound to loopback)
web_password_hash = "$2a$..." # bcrypt; generated by `lentovodec passwd`
api_key = ""                 # key for scripts (X-API-Key); empty = disabled
session_ttl = "72h"
webhook_url = ""              # POST notification when daemon tasks finish;
                              # empty = disabled
webhook_timeout = "10s"       # delivery timeout (one retry on failure)

# --- Cartridge capacity (part planner) ---
capacity = "2.2T"            # estimated cartridge capacity (LTO-6 — with a
                              # safety margin); absent/0 — single-cartridge
                              # behavior
min_tail = "100G"            # when the remaining space drops below this
                              # threshold, a new session starts on a new
                              # cartridge; default — 5% of capacity

[[jobs]]
Name = "media"
Description = "Media library backup"
Mode = "append"              # append | mirror
Paths = ["/tank/data/media"]
Exclude = ["**/.DS_Store", "**/*.partial"]
span_depth = 1               # split spanning parts along 1st-level
                              # directories (subtree locality on one
                              # cartridge); 0/absent — split by files
```

Layering: defaults → TOML → env (`LENTOVODEC_DEVICE`, `LENTOVODEC_DB`,
…) → CLI flags. Secrets (`web_password_hash`, `api_key`) cannot be passed
through env — TOML with 0600 permissions only.

---

## Access and deployment

Managing the drive **does not require root**: `/dev/nst*` is an ordinary
character device (group `tape`), and tape ioctls do not require
`CAP_SYS_RAWIO`. The access criterion is a successful open of the device
(a probe at startup of commands and the daemon), not `uid == 0`; on
`EACCES`/`ENOENT` you get a clear error with a hint (`usermod -aG tape
<user>`).

The daemon and all local commands must run as the **same** user: in WAL
mode SQLite keeps `db-wal`/`db-shm` next to the database file.

A typical Linux deployment is a dedicated `lentovodec` user (group
`tape`), data in `/var/lib/lentovodec`, config in `/etc/lentovodec`, and
a hardened systemd unit (`ProtectSystem=strict`,
`SystemCallFilter=@system-service`, etc.):

```bash
useradd --system --home-dir /var/lib/lentovodec --create-home \
        --shell /usr/sbin/nologin lentovodec
usermod -aG tape lentovodec
```

The full guide (udev, including the Arch Linux quirk — the `storage`
group instead of `tape` on `/dev/nst*`; data directories, a hardened
systemd unit, verifying the setup on a real drive) is in
[`docs/func/EN/os-setup.md`](docs/func/EN/os-setup.md).

---

## CLI, API, and Web UI

| Interface | In short | Details |
|---|---|---|
| **CLI** | `backup`, `restore`, `tape`, `jobs`, `catalog`, `passwd`, `daemon`; local commands access the tape directly, daemon commands go over HTTP | [`docs/func/EN/cli.md`](docs/func/EN/cli.md) |
| **REST API** | `/api/*`: authentication, tape, jobs, background tasks, catalog | [`docs/func/EN/api.md`](docs/func/EN/api.md) |
| **Web UI** | Login, Tape, Jobs, Catalog, Files screens; RU/EN; embedded into the binary (`go:embed`) | [`docs/func/EN/api.md`](docs/func/EN/api.md) |

The daemon is a "single-machine tool": by default it listens on
`127.0.0.1:29201` and is not visible from the network. Managing it from
another machine is done through an SSH tunnel:

```bash
ssh -L 29201:127.0.0.1:29201 lentovodec@server
# then on your laptop: http://localhost:29201
```

Direct LAN access is enabled by changing `bind`;
the daemon then requires a configured password and refuses to
start without one.

---

## Project structure

```
.
├── cmd/lentovodec/          # Entry point: wiring (~50 lines)
├── internal/
│   ├── domain/              # Pure types: Job, FileMeta, Session, TapeLabel, errors
│   ├── port/                # Interfaces: Tape, Filesystem, Catalog, Clock, Rand, …
│   ├── usecase/             # Orchestration: scan, backup, restore, format, catalog
│   ├── adapter/             # Port implementations: tapeformat, linuxtape, filetape,
│   │                        # osfs, sqlite, tomlconfig, xxhash, sloglog
│   ├── iface/               # Delivery: cli (cobra), web (chi + REST + embed), destfs
│   └── testutil/            # Shared test doubles (FakeTape, MapFS, MemCatalog, …)
├── test/
│   ├── integration/         # End-to-end scenarios on real adapters (no hardware)
│   └── hardware/            # //go:build tape — runs on a real drive
├── web/                     # Vue 3 + Vite sources (bundle → iface/web/assets)
├── docs/                    # func/EN/ and func/ru/ — user documentation;
│                            # ARCHITECTURE/SPECIFICATION/FORMAT/… — for development
├── .forgejo/workflows/      # CI: build, tests, release publishing
├── LICENSE                  # GNU GPL v3
└── README.md                # Main documentation
```

The business logic (`domain`, `usecase`) does not import `os`, `syscall`,
`net`, or concrete adapters — all I/O goes through `internal/port`
interfaces; the rule is enforced by the `depguard` linter.

---

## Technology stack

**Backend:** Go 1.26 · cobra / viper · chi v5 · modernc.org/sqlite (no
CGO) · cespare/xxhash/v2 · golang.org/x/crypto (bcrypt) ·
bmatcuk/doublestar/v4 · `log/slog` · `go:embed`.

**Frontend:** Vue 3 (Composition API) · Vite 7 · TypeScript 5.9
(vue-tsc).

**Infrastructure and quality:** Forgejo Actions (CI) · golangci-lint
(strict config, depguard) · `go vet` / `gofmt` · `go test` (including
`-race`) · unit / integration / hardware test levels.

---

## Development

| Command | Purpose |
|---|---|
| `make lint` | `golangci-lint run ./...` (strict config) |
| `make vet` | `go vet ./...` |
| `make test` | `go test ./...` |
| `make test-race` | `go test -race ./...` |
| `make cover` | Coverage report |
| `make build` | Build `bin/lentovodec` |
| `make build-tape` | Build with the `tape` tag (real drive driver) |
| `make web-build` | Vite build of the Web UI into `internal/iface/web/assets/` |
| `make web-dev` | Vite dev server proxying `/api` to `:29201` |
| `make clean` | Remove `bin/`, `coverage/`, the web bundle |

On a fresh clone, run `make web-build` first (the bundle is embedded via
`//go:embed`), then `make build`.

Developer documentation (reading order before making changes):

1. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — layers, import rules.
2. [docs/SPECIFICATION.md](docs/SPECIFICATION.md) — requirements, CLI, REST API, DB schema.
3. [docs/FORMAT.md](docs/FORMAT.md) — the tape format byte canon.
4. [docs/TESTING.md](docs/TESTING.md) — testing strategy.
5. [docs/ROADMAP.md](docs/ROADMAP.md) — the plan and stage history.

---

## License

The project is distributed under the **[GNU General Public License v3.0](LICENSE)**.

```
Lentovodets — tape backup system (LTO archiver with catalog and Web UI)
Copyright (C) 2026  AlexRus1234

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
```

[^1]: The source code was developed with an AI assistant according to the
predefined project architecture; architectural decisions, result
verification, and final integration were performed by the author.
