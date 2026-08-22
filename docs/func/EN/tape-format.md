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

# Tape Format

This document describes the `LENTOVODEC_TAPE_V2` format from the
operator's point of view: what lies on the cartridge, why it is
reliable, and what it is compatible with. The byte canon (JSON
structures, tar header conventions, the exact rewinding rules) is in
[`docs/FORMAT.md`](../../FORMAT.md).

---

## Overview

A tape is a sequence of **fixed-size blocks** (256 KiB) and
**filemarks** — markers written by the `MTWEOF` command. Every logical
unit (the label, a session index, a session tar stream) ends with a
filemark.

```
                block positions                    filemark #
                ─────────────────                  ──────────
    ┌──────────────────────────┐
    │  LABEL  (1 block 256 KiB) │ ─── EOF ───────────────── 1
    ├──────────────────────────┤
    │  SESSION 1 INDEX         │ ─── EOF ─────────────────── 2
    ├──────────────────────────┤
    │  SESSION 1 TAR           │ ─── EOF ─────────────────── 3
    ├──────────────────────────┤
    │  SESSION 2 INDEX         │ ─── EOF ─────────────────── 4
    ├──────────────────────────┤
    │  SESSION 2 TAR           │ ─── EOF ─────────────────── 5
    ├──────────────────────────┤
    │  ...                     │
    ├──────────────────────────┤
    │  SESSION K INDEX         │ ─── EOF ─────────────────── 2K
    ├──────────────────────────┤
    │  SESSION K TAR           │ ─── EOF ─────────────────── 2K+1
    ├──────────────────────────┤
    │  (empty, EOD)            │ ─── EOF ─── EOF ─────────── 2K+2, 2K+3
    └──────────────────────────┘
```

**The key invariant:** a cartridge with `K` sessions has exactly
`2K + 3` filemarks — two per session, one after the label, and a double
one at the end (EOD). This makes positioning deterministic: the start
of session K's index is `MTFSF(2K−1)`, the start of its tar is
`MTFSF(2K)`. In legacy nil-backup, filemarks between sessions were not
written, which made "smart" restore fragile — this is fixed here and
pinned by tests.

---

## Cartridge label

The first block is JSON (zero-padded to the block size):

```json
{
  "magic":          "LENTOVODEC_TAPE_V2",
  "format_version": 2,
  "name":           "media-001",
  "uuid":           "550e8400-e29b-41d4-a716-446655440000",
  "formatted_at":   "2026-08-13T12:34:56Z"
}
```

- `name` — the human-readable cartridge name, unique in the catalog;
- `uuid` — generated at format time, linking the cartridge to catalog
  records (sessions, file copies);
- on read the label is validated: a blank cartridge, a foreign format,
  a newer format version — each is a distinct typed error.

## Session: index + tar

Each session is an "JSON index + tar stream" pair:

- **The index** — session metadata (number, FULL/INC type, time, job)
  and the file list: path, size, mtime, directory flag, xxhash64,
  state (`A`/`M`/`D`). Reading the index tells you the session contents
  without reading the data.
- **The tar stream** — the file contents (GNU format). Tombstones (`D`,
  deletion records in mirror mode) do not go into the tar — only into
  the index.

## Integrity

- For every file, the index stores `hash = hex(xxhash64(contents))`.
- A file counts as successfully read **only if** its hash matched — on
  every restore (full/selective/smart) and on readtest.
- The encoder checks the hash on write too: a file that changed between
  the scan and the write is an error, not a silent corruption of the
  index.

## Full and INC

- **FULL** (`--full` or the first session on a cartridge): rewind to
  the beginning, a new chain from session 1; subsequent sessions are
  overwritten and old catalog records purged. The label is kept (the
  UUID and the name do not change).
- **INC** (default): append at end-of-data (EOD), number = last + 1.

The session format is identical in both cases.

## Multi-volume backups and the continuation pointer

A session that does not fit onto a cartridge whole is split at file
boundaries into **parts** — each becomes a self-contained complete
session on its own cartridge: its own label, its own index, its own
tar with all the trailers. No byte-level splicing between cartridges;
a file is never cut in half.

A cartridge with an unfinished chain is closed with a **continuation
pointer block** — a one-block JSON record before the closing filemark
pair:

```
… [part tar][EOF] [block {"kind":"continuation", …, "next_tape_name":"media-014"}] [EOF][EOF]
```

- The parts of one run are linked by shared metadata: the same run
  identifier, the part number in the index (`part`), and the
  `continues` back-reference to the previous cartridge's UUID — the
  chain is readable in both directions.
- The "2K+3 filemarks" invariant holds with the pointer too: it lies
  between the filemark of the last part's tar and the closing pair.
- Corruption of one cartridge of the chain loses only its files: the
  other parts are independent archives.
- The changes are additive: old binaries read new tapes and vice
  versa; the format version was not bumped.

What a multi-volume backup looks like for the operator —
[features.md](features.md); the CLI — [cli.md](cli.md).

## DR without Lentovodets: plain tar

Every cartridge of the chain restores on its own, with any GNU tar:

```bash
mt -f /dev/nst0 rewind
mt -f /dev/nst0 fsf 2              # past the label and the part index
dd if=/dev/nst0 bs=256k | tar -x   # for every cartridge of the chain
```

`dd` reads up to the filemark — exactly the part's tar segment. The
multi-volume mode `tar -xM` does not fit: the label precedes the tar,
and a volume change requires reopening the device from the beginning
of the tape.

## Versioning and compatibility

- Any format change bumps `format_version` in the label.
- Reading: an equal version — OK; older — supported with a warning in
  the log; newer — the operation is aborted (`ErrNewerFormat`).
- A `magic` not starting with `LENTOVODEC_TAPE_` — a foreign format
  (`ErrForeignFormat`). **Legacy `nil-backup` cartridges are
  deliberately not read**: old data is for the old binary, or a
  conscious re-format with `--force`.

## The filetape emulator

For development and CI without a drive, the same stream of blocks and
filemarks can live in a regular file: the `FTAPEV1\0` header + frames
of "type | length | data" (a block or a filemark). Tape semantics are
preserved — writing into the middle truncates the tail, like on a real
tape; just pass `--device /path/to/file`.
