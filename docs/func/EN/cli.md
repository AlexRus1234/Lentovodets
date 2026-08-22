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

# CLI Reference

---

## Global flags

Inherited by all subcommands:

| Flag | Default | Description |
|---|---|---|
| `--config PATH` | `./lentovodec.toml` | Path to the config |
| `--device PATH` | `/dev/nst0` | Tape device (see below) |
| `--db PATH` | `./lentovodec.db` | Path to the SQLite catalog |
| `--log PATH` | `./lentovodec.log` | Log file |
| `--server URL` | `http://127.0.0.1:29201` | Daemon address for daemon commands |
| `-v`, `--verbose` | — | Debug logging |

### Tape device selection

A build with the `tape` tag (Linux) picks the implementation by path
type:

| `--device` | Implementation |
|---|---|
| Character device (`/dev/nst0`) | Real drive (`linuxtape`, `MTIOCTOP` ioctls) |
| An existing regular file | The `filetape` emulator |
| A non-existent path outside `/dev` | A new file tape is created |
| A non-existent path under `/dev` | Probe error (the drive is not connected) |

A build without the `tape` tag always uses `filetape` — running on
Windows/macOS and in CI needs no hardware.

---

## Command modes

| Mode | How it works |
|---|---|
| `local` | Direct access to the tape and the catalog; no daemon needed |
| `daemon` | The command goes to the HTTP API (`--server`): `api_key` from TOML, on 401 — interactive login and a Bearer token |
| `server` | Listens on HTTP itself (`daemon`) |

The mode boundary is explicit: local commands require physical access
to the drive on the same machine; daemon commands require a running
daemon.

---

## Commands

### Backup

```bash
lentovodec backup <job> [--full] [--dry-run] [--verify] [--next-tape NAME]
```

| Flag | Description |
|---|---|
| `--full` | Full backup: rewrite the chain from session 1 (old sessions are purged from the catalog) |
| `--dry-run` | Scanning and statistics only, nothing written |
| `--verify` | Read the written data back and verify hashes right after writing (roughly ×2 the time) |
| `--next-tape` | Name of the next cartridge for non-interactive spanning (scripts, cron) |

Example:

```console
$ lentovodec backup media
session #4 INC (id 12) on cartridge 550e8400-e29b-41d4-a716-446655440000
changed 37: added 30, modified 5, deleted 2; to write 1.2 GiB
```

The first session on a cartridge is always FULL (the flag is not
needed). On a write failure the created session is deleted from the
catalog (compensation); the index and the tape stay consistent.

#### Verify-after-write (`--verify`)

LTO ECC protects bits, not content: adjacent-track overwrites and servo
data errors are real-world stories. `--verify` re-reads the freshly
written sessions and checks every file's xxhash against the index (the
hashes are already computed — the check is cheap in code, expensive in
time: roughly ×2 the write duration). Only the sessions of the current
run are checked, not the whole cartridge (full media diagnostics —
`tape readtest`); with spanning, each part is verified on its own
cartridge before the change.

```console
$ lentovodec backup media --verify
session #4 INC (id 12) on cartridge 550e8400-e29b-41d4-a716-446655440000
verified: 37 files (1.2 GiB)
changed 37: added 30, modified 5, deleted 2; to write 1.2 GiB
```

A verification failure does not roll the session back: the data is
already on tape and in the catalog, and rewriting does not cure the
medium. The command fails with "session N was written but does not read
back" — the operator decides (cartridge repair/replacement, a re-backup).

#### Multi-volume backup (spanning)

If `capacity` is set in the config and the session does not fit on one
cartridge, it is written as parts onto several cartridges. In terminal
mode the tool itself asks when a cartridge closes:

```console
$ lentovodec backup media
Cartridge media-013 closed. Insert a blank cartridge, name [media-014]:
session #1 FULL (id 21) on cartridge 8f14e45f-ceea-467f-af26-2d63b9a4c95a
parts 2 on cartridges: media-013, media-014
changed 512: added 512, modified 0, deleted 0; to write 3.9 TiB
```

- Enter accepts the suggested name — an increment of the numeric suffix
  (`media-013` → `media-014`); you can type your own.
- The new cartridge is formatted and registered in the catalog
  automatically; the inserted tape must be blank.
- Without a terminal (cron, scripts) set the name with the flag:
  `lentovodec backup media --next-tape media-014`. For a third
  cartridge and beyond the flag will not help — spanning is
  interactive.
- A cartridge that fills up mid-part is closed with a continuation
  pointer, and the part is moved wholesale to the next cartridge. If a
  part does not fit onto a whole cartridge either — decrease
  `capacity` in the config.

### Restore

```bash
lentovodec restore [--paths p1,p2] [--dest DIR] [--original]
```

| Flag | Description |
|---|---|
| `--paths` | Comma-separated paths → smart restore (freshest copy, fallback to older ones); empty — full restore of the whole cartridge |
| `--dest` | Restore into the given directory instead of original paths; `..` paths from the tape index are rejected and writing through restored symlinks is forbidden — nothing gets written outside the restore directory |
| `--original` | Restore to the original paths from the index (ignores `--dest`) |

Examples:

```bash
lentovodec restore --dest /safe                    # whole cartridge into /safe
lentovodec restore --paths /tank/media/a.mkv --dest /safe   # smart by path
lentovodec restore --paths /tank/media/dir --dest /safe     # smart by subtree
lentovodec restore --original                      # to original paths (overwrites!)
```

Selective restore from a specific session is available in the Web UI
(session file browser → "Restore selected").

#### Restoring a cartridge chain

Full restore (`restore` without `--paths`) follows the continuation
pointers of a spanning chain. In terminal mode:

```console
$ lentovodec restore --dest /restore
Cartridge media-013 read. Insert cartridge media-014 (part 2) and press Enter:
restored 512 files, 34 directories, skipped 0 (sessions read 2)
```

- No catalog needed — the chain is read from the tapes themselves (a DR
  scenario).
- The inserted cartridge is verified by its label name and its
  back-reference to the previous one; the wrong cartridge — a
  `ChainMismatchError` before any data is restored. Insert the correct
  one and re-run the command.
- Without a terminal the restore stops at the cartridge boundary with a
  warning: insert the next cartridge and re-run — the files of the read
  part are already in place.
- Selective/smart work within the cartridge in the drive; a session of
  part N requires that part's cartridge — on a miss the error prompts
  which cartridge to insert.

### Tape

| Command | Mode | Description |
|---|---|---|
| `lentovodec tape format <name> [--force]` | local | Format a cartridge: label (UUID, name) + double EOF + a catalog entry. `--force` — re-format an already formatted one |
| `lentovodec tape readtest` | local | Diagnostic read of the whole cartridge with hash verification, nothing written to the FS; for spanning chains — a report per cartridge (chain following is the same as for restore) |
| `lentovodec tape info` | daemon | Read the label and the active TapeAlert flags of the current cartridge |
| `lentovodec tape eject` | daemon | Eject the cartridge (MTOFFL) |

### Jobs

| Command | Description |
|---|---|
| `lentovodec jobs list` | Show jobs from TOML |
| `lentovodec jobs add <name> --paths P1,P2 [--mode M] [--desc D] [--exclude E1,E2] [--span-depth N]` | Add a job to TOML |
| `lentovodec jobs remove <name>` | Remove a job |

`--mode`: `append` (default) or `mirror`; the semantics of the modes
and exclude patterns — [features.md](features.md).

`--span-depth N` — the directory nesting depth for grouping spanning
parts (0 — the default, splitting by files; the key is not written to
TOML):

```bash
# "movies on LTO-001": 1st-level subtrees are not torn between cartridges
lentovodec jobs add media --paths /tank/data/media --span-depth 1
```

### Catalog (daemon)

| Command | Description |
|---|---|
| `lentovodec catalog tapes` | List cartridges |
| `lentovodec catalog sessions [--tape UUID]` | List sessions (with a cartridge filter); spanning chain parts carry a `part N` suffix |
| `lentovodec catalog files --session N` | Files of a session |
| `lentovodec catalog search <pattern>` | Substring file search |
| `lentovodec catalog copies <path>` | All copies of an exact path, newest first |
| `lentovodec catalog rm --session N` | Delete a session from the catalog (the data on the tape remains) |
| `lentovodec catalog prune --days N` | Delete sessions older than N days |

The exception is `catalog rebuild`, which runs locally (direct access
to the tape and the catalog, no daemon needed):

| Command | Mode | Description |
|---|---|---|
| `lentovodec catalog rebuild` | local | Rebuild the catalog from the indexes of the inserted cartridge (DR) |

`rebuild` transfers the cartridge label and the JSON indexes of all its
sessions into the database (fast — the tar stream is not read and the
hashes are not checked; that is done by `tape readtest`). Scenarios:
a lost or damaged `lentovodec.db`, a cartridge unknown to the catalog
(backed up by another machine/DB), migration to a new machine.
Re-running is safe: existing sessions are skipped, nothing is
overwritten; rebuild can be run against a live catalog too — it fills
in the missing parts (but do not run it in parallel with a backup:
there is a single drive). A cartridge with a chain continuation ends
with a hint:

```text
the chain continues: insert cartridge media-014 and re-run rebuild
```

by which time the data of the read part is already in the catalog.

### Miscellaneous

| Command | Mode | Description |
|---|---|---|
| `lentovodec passwd` | local | Ask for a password (twice) and print the bcrypt hash for `web_password_hash` in TOML |
| `lentovodec daemon [--port N] [--bind ADDR]` | server | Start the HTTP daemon (REST API + Web UI) |

The `--port`/`--bind` flags override the corresponding part of `bind`
from TOML (a non-loopback address requires a configured password —
otherwise the daemon refuses to start).

---

## Typical scenarios

```bash
# A new cartridge → the first full backup → a couple of increments:
lentovodec tape format LTO-001
lentovodec backup media                # session 1, FULL
lentovodec backup media                # session 2, INC
lentovodec backup media --full         # a new chain: 1 again, FULL

# Medium check before shelving a cartridge into the archive:
lentovodec tape readtest

# lentovodec.db lost — the catalog is rebuilt from the cartridges:
lentovodec catalog rebuild

# Disaster recovery after losing the filesystem (a cartridge chain —
# Lentovodets itself will ask for the next one):
lentovodec restore --dest /restore

# One file into a safe place:
lentovodec restore --paths /tank/media/movie.mkv --dest /safe
```

The daemon REST API — [api.md](api.md); deployment —
[os-setup.md](os-setup.md).
