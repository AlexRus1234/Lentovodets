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

# Changelog (English translation)

Format — [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning — semver. The canonical file is
[CHANGELOG.md](CHANGELOG.md) (Russian); this file is its translation
and may lag slightly behind. Stage-by-stage development history with
implementation decisions (in Russian) —
[docs/HISTORY.md](docs/HISTORY.md).

## [Unreleased]

### Changed

- CI: dnf in build-test goes exclusively through Khrazhevnik
  (dogfood — default fedora metalink repos removed), the fedora image
  pulled via the Nora mirror; Playwright E2E installs the system
  chromium instead of the npm package (runner disk space).
- CI: build-test toolchain refresh — fedora:46 image, Go 1.27.1,
  actions/checkout@v7; the local lint gate moved to golangci-lint v2
  (`.golangci.yaml` migration, behavior unchanged).

## [1.0.1] — 2026-08-27

### Added

- Webhook notifications about background task completion:
  `webhook_url` and `webhook_timeout` in TOML. A generic JSON payload
  is sent with the task state, error, statistics, tapes and
  timestamps; delivery is asynchronous with a single retry and never
  affects the backup result.

- `tape info` and the Web UI show active drive TapeAlert flags via
  SCSI LOG SENSE page 0x2e: cleaning required, media life and
  read/write errors. SG_IO unavailability does not interfere with
  label reading.

- File copy history in the Web UI and via
  `GET /api/catalog/file-copies?path=`; every version shows date,
  tape, session, size and hash and can be restored selectively.
  New CLI command `catalog copies <path>`.

- Verify-after-write: `--verify` on `backup` (CLI) and `verify=true`
  on `POST /api/backup/start` — freshly written sessions are
  immediately read back, each file's xxhash is checked against the
  index, and the contents are tallied against what was written. Why:
  LTO ECC ≠ content verification (adjacent track overwrites,
  servodata errors — real-world stories); the hashes are already in
  the index, the check is free in code and costly in time (~×2 of
  write duration). Only sessions of the current run are verified, not
  the whole cassette (full media diagnostics is `tape readtest`);
  every part of a spanning chain is verified on its own cassette
  before the change. A verification failure is a typed `VerifyError`:
  the data is already on tape and in the catalog, no rollback is
  performed (rewriting does not heal the medium), the session stays
  recorded, the run reports "session N written but not readable
  back" — the operator decides (clean/replace the cassette, rerun the
  backup). Off by default. Progress gained a `verify` phase; the
  `backup finished` log line gained `verify`/`verified_files`/
  `verified_bytes` fields; the CLI prints "verified: N files".
- `lentovodec catalog rebuild` (local): catalog reconstruction from
  the contents of an inserted cassette — the label and JSON indexes
  of all sessions are transferred into the DB, the tar stream is not
  read (rebuild ≠ verification, integrity is checked by
  `tape readtest`). DR scenarios: lost/corrupted `lentovodec.db`, a
  cassette unknown to the catalog, moving to a new machine.
  Idempotent (existing sessions are skipped, works on a live catalog
  too); a cassette continuing a chain finishes with a hint to insert
  the next one and rerun. New codec method `ReadIndexFiles` (header +
  index files without tar). REST/UI button and automatic tape
  changing — backlog.
- Symbolic/hard links are saved and restored; unsupported special
  files are skipped and counted in the statistics.
- The file browser now sees a symlink to a directory as a link, not
  as a directory: `osfs.Stat` uses lstat semantics to avoid
  dereferencing links.

- Server-side file browser in the Web UI: `GET /api/fs/list` with
  shared authentication, breadcrumbs, directory and file listing,
  multi-select of job roots and restore destination selection.
  Access is performed on behalf of the daemon; a roots allowlist is
  not introduced yet.

- Spanning part cuts by directories: per-job key `span_depth`
  (`[[jobs]]`, int, default 0 — the former per-file cutting, existing
  configs unchanged). N ≥ 1 — parts never tear apart a directory
  group of level N under the job root: a group that does not fit
  rolls back entirely to the start of the next part ("movies on
  LTO-001" — a whole subtree on one cassette, selective restore of a
  subtree touches fewer cassettes); a group larger than a whole
  cassette is cut by files inside it (fallback). CLI:
  `jobs add --span-depth N` (0 — the key is not written). Directories
  and tombstones never tear a group apart; `min_tail`/ENOSPC
  behavior unchanged. The `backup planned` log line gained a
  `span_depth` field.

- Spanning in the daemon: backup/restore across a cassette chain
  without a terminal. A task at a cassette boundary enters the
  `awaiting_tape` state (the progress object gains a `message` for
  the operator and `suggested_tape_name`), the Web UI shows a
  continuation dialog with a pre-filled tape name. Resumption —
  `POST /api/tasks/{id}/continue` with body `{"tape_name": "..."}`
  (empty — the suggested name; with a session or `X-API-Key` —
  scripted change; audit `event=task_continue`). A new backup
  cassette is formatted and registered automatically, a dirty one —
  a clear error with a repeat continue; a restore cassette is checked
  by label and the chain's back-reference. A waiting task occupies
  the drive (409 on a parallel start and device change); graceful
  shutdown closes the wait immediately — the task fails with a
  cancellation error rather than a timeout. No cancel endpoint was
  introduced: a waiting task can be cancelled by stopping the daemon.

- Restore and verification across a spanning cassette chain:
  `restore` (without `--paths`) and `tape readtest` follow the
  continuation pointers — an interactive prompt "Cassette X read.
  Insert cassette Y (part N) and press Enter:", the inserted cassette
  is checked by label name and the `continues` back-reference of its
  first session; wrong cassette — the typed `ChainMismatchError`
  before any data is restored (rerun after inserting the right
  cassette continues). The catalog is not needed for following the
  chain (DR), a best-effort chain-length report — when available.
  Without interactive input (scripts), a continuation pointer ends
  the operation with a warning "insert the cassette and restart";
  the read part's data is already restored/verified. `tape readtest`
  prints a report per cassette of the chain
  (sessions/files/bytes). Selective restore of a part's session
  requires that part's cassette in the drive — on a miss the error
  suggests which cassette to insert.
- Local-mode spanning backup: a session that does not fit a cassette
  (the `capacity`/`min_tail` planner) is written in parts across
  several cassettes — with an interactive change: "Cassette X
  closed. Insert a blank cassette, name [Y]:" (Enter accepts the
  suggestion — a numeric suffix increment). For non-interactive runs
  — the `--next-tape NAME` flag; every new cassette is formatted and
  registered automatically. A cassette filling up mid-part
  (`TapeFullError`) moves the whole part to the next cassette,
  restoring the filled one's EOD; two consecutive failed attempts at
  full capacity — an error recommending to lower `capacity`. The
  `backup` output gained "parts N on cassettes: …" lines.
- Catalog: a session knows its part number in a spanning run
  (`part`, a regular session is part 1). Existing DBs migrate
  automatically (`PRAGMA user_version` 0→1, old rows get `part = 1`);
  REST `/api/catalog/sessions` returns the `part` field,
  `catalog sessions` shows a `part N` suffix for parts >1. Parts of
  one run are linked by the existing `job_run_id`.
- Tape format: spanning groundwork — a session index may carry a
  part number (`part`) and a back-reference to the previous cassette
  of the chain (`continues`); after the closing filemark pair a
  JSON continuation pointer block to the next cassette is possible.
  The changes are additive: an old binary ignores the new fields, a
  new one reads old tapes.
- Config `capacity`/`min_tail` (TOML/env): cassette capacity estimate
  and tail threshold for the part planner. With `capacity` set, a
  file larger than the cassette (or its remainder) — an honest error
  after the scan, before writing to tape; the `backup planned` log
  line gained `planned_parts` and `budget_bytes`.

### Changed

- Tape error classification switched from parsing message texts to
  typed errors (`TapeFullError`, `NoMediumError`,
  `SessionDamageError`): message wording no longer affects behavior
  (damaged-session skip, daemon 409 responses).

- Smart restore walks sessions globally from newest to oldest (by
  session number), not in the order paths are listed.

- The CLI HTTP client has a request timeout (5 minutes) and a clear
  error when the daemon's response exceeds 1 MiB instead of silently
  truncated JSON; `POST /api/tasks/{id}/continue` limits the request
  body, the server sets `ReadHeaderTimeout`.

- The CI release artifact is built with the tape driver
  (`-tags tape`): the release binary controls the drive directly;
  the filetape build remains for integration tests without a drive.

- Tests: Playwright E2E for the Web UI (`make test-e2e`, opt-in in
  CI via `run_e2e_tests`): an end-to-end scenario format → job →
  backup → progress → sessions → files → smart-restore to dest
  against a real daemon on filetape.

- Documentation: English translation of the README and the user
  guide set (`docs/func/EN/`: features, cli, api, os-setup,
  tape-format); a note at the top of the README (ru/en) — "a
  cassette is readable by plain GNU tar without Lentovodets" with a
  DR recipe.

- Docs: preparing a cassette with foreign layout for formatting
  (`mt-st weof`) and read/write troubleshooting (EROFS/EACCES from
  systemd hardening, the restore sandbox) —
  `docs/func/EN/os-setup.md` (and the Russian original).

### Fixed

- Selective/smart restore writes only the requested paths (and their
  subtrees) to disk: previously the whole session was unpacked,
  `paths` only affected the statistics. The hashes of the remaining
  entries are still verified while reading.

- A forced FULL backup (`--full`) positions at session 1
  (FORMAT §10) instead of appending after old sessions: the next
  incremental session no longer overwrites the just-written FULL.

- Restore to a destination directory (`--dest`) rejects `..` paths
  from the tape index and forbids writing through restored symlinks:
  a damaged or malicious cassette cannot write files outside the
  destination directory.

- The `exclude` parameter no longer produces mirror tombstones for
  live files: adding an exclude pattern between mirror backup runs
  does not delete those files on mirror restore.

- A mid-spanning-run failure rolls back the already recorded parts
  from the catalog: a partial chain with a continuation pointer does
  not linger in the catalog.

- An interrupted/corrupted file restore does not leave a partial
  file on disk; a truncated tar stream is detected by the mismatch
  with the index instead of being read as success.

- SQLite: `foreign_keys`/`busy_timeout` PRAGMAs are set in the DSN
  and hold on any pool connection; the TOML config is written
  atomically (temp file + rename), editing jobs from Web/CLI is
  thread-safe.

- Fixed TapeAlert parameter numbering `0x0800–0x083F` (`0x800` =
  flag 1), active value-bit checking and display of the low flags
  (`READ FAILURE`, `WRITE FAILURE`, `HARD ERROR`, `MEDIA`, `WORM`).
  Added SG_IO status checks and safe ioctl buffer handling.

- A cassette label with an invalid `formatted_at` (not RFC-3339) is
  no longer read silently: validation moved into `DecodeLabel` (the
  domain helper `TapeLabel.ParseFormattedAt`) — a broken label of
  our format yields an honest error for every reader (`tape info`,
  restore, rebuild), not only during catalog reconstruction.
  `TapeInfo` was also consolidated onto a shared `readLabel`
  (a third duplicate of label reading removed).
- Drive access race in the daemon: the Web UI and API opened
  `/dev/nst*` from several places without coordination (the 15s
  status probe, tape info/eject/format, background tasks), while the
  st driver allows a single open descriptor — parallel requests got
  `EBUSY` ("device or resource busy"). Access is serialized
  (tapeGate): short tape operations during a background task get an
  immediate `409 task_running`, the status probe is not blocked (a
  device occupied by a task counts as available); a drive without a
  cassette — `409 no_medium` with the text "no cassette in the
  drive" instead of the raw "no medium found" (web and CLI).
- A write failure mid-session (`TapeFullError`/ENOSPC) no longer
  leaves a "dirty tail" behind the old EOD on tape: the partial
  write is rolled back (`Rewind + MTFSF(2K+1) + WriteEOF×2` —
  writing at the position truncates the remainder), the tape stays
  fit for appending, the session is deleted from the catalog.
  Closed a long-standing latent bug of version 1.0.0.
- Backup statistics (`Stats.Bytes`) no longer include Lstat
  directory sizes (on Linux they are ≠ 0 and platform-dependent); a
  directory's size in the index is always 0, directory changes are
  detected by mtime.
- `restore --dest` correctly handles paths with a volume name
  (`C:/data`) when restoring on an OS different from the backup OS.
- `linuxtape` driver: mt commands (rewind/eject/format etc.) no
  longer fail with `EINTR` from a stray signal (including SIGURG
  from the Go scheduler) — the ioctl is retried; interruption is
  only possible before the command starts, the retry is safe.
- Smart restore: a dest write error aborts the copy walk with an
  honest error instead of masquerading as "all copies damaged"
  (`NoHealthyCopyError`) — an operator without journalctl got a
  false verdict of medium corruption when the cause was permissions
  or a read-only destination.
- Web UI "Files": the tree for a job with absolute roots
  (`/tank/...`) no longer collapses into a nameless "/" root;
  checkboxes send the original catalog paths to the API (not
  normalized ones) — a selection mismatch no longer looks like "all
  copies damaged".
- Task speed in the Web UI is computed as a per-phase average
  instead of an EWMA over instantaneous samples: readings like
  "371 MiB/s" at the physical LTO-4 limit (~120 MB/s) are gone;
  resetting the base on phase/session change removes negative values
  during Full-restore.

## [1.0.0] — 2026-08-16

First release: stages 0–10 complete.

- Tape format `LENTOVODEC_TAPE_V2`: JSON label, sessions of
  "JSON index + tar" with filemark invariants, xxhash64 of every
  file (see [FORMAT.md](docs/FORMAT.md)). Legacy cassettes are not
  read.
- CLI: `backup`, `restore` (full/selective/smart), `tape
  format/readtest/info/eject`, `jobs list/add/remove`, `catalog
  tapes/sessions/files/search/rm/prune`, `passwd`, `daemon`.
- Daemon: REST API (SPEC §6) + Web UI (Vue 3, `embed.FS`);
  bcrypt login, TTL sessions, rate-limit, api_key; default bind
  `127.0.0.1:29201`.
- Rootless model: drive access via the `tape` group, no root needed
  (SPEC §9.1); deployment — README.
- SQLite catalog (WAL); job modes `append`/`mirror` (tombstones).

[Unreleased]: https://git.yadr00.internal/AlexRus1234/Lentovodets/compare/v1.0.1...HEAD
[1.0.1]: https://git.yadr00.internal/AlexRus1234/Lentovodets/releases/tag/v1.0.1
[1.0.0]: https://git.yadr00.internal/AlexRus1234/Lentovodets/releases/tag/v1.0.0
