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

# Linux Deployment

Lentovodets manages a tape drive through `/dev/nst*` and keeps an
SQLite catalog on disk. Deployment comes down to three decisions: the
process user, device permissions, and the autostart method.

---

## The rootless model

Managing the drive **does not require root**:

- `/dev/nst*` is an ordinary character device (group `tape`); tape
  ioctls do not require `CAP_SYS_RAWIO`.
- **The access criterion** is a successful open of the device (a probe
  at the startup of the daemon and local commands), not `uid == 0`. On
  failure (`EACCES`/`ENOENT`) you get a clear error with a hint
  (`usermod -aG tape <user>` or a udev rule/ACL), not a "not running as
  root" warning.
- A privileged helper (polkit/udisks-style) was considered and
  rejected: one machine, one drive.

Root may be needed only for filesystem paths closed to the given user
by permissions (for example, backing up all of `/etc`) — that is an FS
limitation, not a drive one.

**Important:** the daemon and **all** local commands must run as the
same user: in WAL mode SQLite keeps `db-wal`/`db-shm` next to the
database, and mixing users would require a shared group-writable
directory.

---

## 1. User and group

The `tape` group exists out of the box in Debian/Fedora but not in
Arch Linux (see §2); create it if missing:

```bash
getent group tape || groupadd -r tape
useradd --system --home-dir /var/lib/lentovodec --create-home \
        --shell /usr/sbin/nologin lentovodec
usermod -aG tape lentovodec     # the group takes effect in a NEW session
```

A check (without re-logging the daemon):

```bash
sudo -u lentovodec test -r /dev/nst0 && echo ok
```

---

## 2. udev and the device group

The default group on `/dev/nst*` depends on the distribution:

| Distribution                             | Default `/dev/nst*` | `tape` group |
| ---------------------------------------- | -------------------- | ------------ |
| Debian, Fedora (upstream systemd)        | `root:tape` 0660     | present      |
| Arch Linux                               | `root:storage` 0660  | **missing**  |

Arch patches the default systemd udev rules
(`0001-Use-Arch-Linux-device-access-groups.patch`): tape devices get the
`storage` group, and the `tape` group is not in the package
at all. The typical first-install problem on Arch: the daemon starts,
but the probe fails with `permission denied`; `ls -l /dev/nst0` shows
`root storage`, and `usermod -aG tape` either refuses (no such group)
or is useless.

Diagnosis:

```bash
ls -l /dev/st0 /dev/nst0     # root:storage → the Arch case
getent group tape            # empty → the group is missing
```

The fix is to bring the device to the project convention (the `tape`
group matches the daemon's error hints and the unit):

```bash
getent group tape || sudo groupadd -r tape
```

`/etc/udev/rules.d/88-tape.rules`:

```
KERNEL=="st[0-9]*|nst[0-9]*", GROUP="tape", MODE="0660"
```

then apply:

```bash
sudo udevadm control --reload
sudo udevadm trigger -w /dev/st0 /dev/nst0
```

`-w` (wait for the event to be processed) matters when verifying:
without it `trigger` is asynchronous, and `ls -l` right after the
command still shows the old group — it looks like "the rule did not
work" although it applied moments later.

The alternative is not to create `tape` and keep Arch's `storage`
(`SupplementaryGroups=storage` in the unit). It works, but the daemon's
hints (`usermod -aG tape ...`) stop matching reality.

A group change on the device is picked up by the daemon on restart
(`systemctl restart lentovodec`): a process's supplementary groups are
fixed at its start.

---

## 3. Config and data

```bash
install -d -m 0750 -o lentovodec -g lentovodec /var/lib/lentovodec
install -d -m 0755 /etc/lentovodec
install -m 0640 -o lentovodec -g lentovodec lentovodec.toml /etc/lentovodec/
```

In TOML:

```toml
db    = "/var/lib/lentovodec/lentovodec.db"
log   = "/var/lib/lentovodec/lentovodec.log"
device = "/dev/nst0"
webhook_url = "https://ntfy.sh/my-private-topic"
webhook_timeout = "10s"
```

The daemon reads the config only; write jobs (`jobs add`) as the same
user with write access to the TOML. When jobs are added through the
daemon's Web UI, the daemon itself needs write access — the documented
variant: add `/etc/lentovodec` to the unit's `ReadWritePaths` and give
the config directory to the daemon user (symptoms and commands — the
"Troubleshooting" section, items 2-3). Secrets
(`web_password_hash`, `api_key`) — TOML with 0600 permissions only;
they cannot be passed through env.

### Task-completion webhook

After a backup or restore finishes, the daemon sends a `POST` with
JSON. For example:

```json
{"event":"task_finished","task_id":"task-ab12cd34","kind":"backup","state":"success","error":"","job":"media","bytes":123,"files":10,"tapes":["LTO-001"],"started_at":"2026-08-21T03:00:00Z","finished_at":"2026-08-21T03:12:00Z","version":"1.x"}
```

For ntfy a topic URL is enough:

```bash
curl -X POST https://ntfy.sh/my-private-topic \
  -H 'Content-Type: application/json' \
  -d '{"event":"task_finished","state":"success"}'
```

No secrets are sent in the v1 webhook: the token can be part of the
URL. Keep the TOML at `0600`. On a network error or a non-2xx response
the daemon makes one retry and then logs a warning; the task result is
unaffected.

---

## 4. systemd unit

`/etc/systemd/system/lentovodec.service`:

```ini
[Unit]
Description=Lentovodets — tape backup daemon
After=local-fs.target

[Service]
Type=simple
User=lentovodec
Group=lentovodec
SupplementaryGroups=tape
StateDirectory=lentovodec
WorkingDirectory=/var/lib/lentovodec
ExecStart=/usr/local/bin/lentovodec daemon --config /etc/lentovodec/lentovodec.toml
Restart=on-failure
RestartSec=5

# --- Hardening ---
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/lentovodec
# jobs add via the Web UI requires write access to /etc/lentovodec —
# add the directory to ReadWritePaths and grant it to the daemon user:
# the "Troubleshooting" section, items 2-3
PrivateTmp=true
# Do NOT enable PrivateDevices: /dev/nst0 access is required
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictRealtime=true
RestrictSUIDSGID=true
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @obsolete
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable --now lentovodec
journalctl -u lentovodec -f
```

Graceful shutdown is built in: SIGTERM → HTTP shutdown (5 s) → waiting
for the active tape task (up to 30 s).

---

## Network access

The daemon is a "single-machine tool"; by default
`bind = 127.0.0.1:29201` and the port is not visible from the network.

**Remote management** — through an SSH tunnel (authentication and
encryption are provided by SSH):

```bash
ssh -L 29201:127.0.0.1:29201 lentovodec@server
# then on the local machine: http://localhost:29201
```

**Direct LAN access** is enabled by changing `bind`
(for example, `192.168.1.10:29201` or `0.0.0.0:29201`, including via
the `--bind`/`--port` flags). The daemon then **requires** configured
authentication: a non-loopback bind without `web_password_hash` and
`web_username` — a startup failure with a diagnostic message. HTTP
without TLS is an accepted compromise for a local network; when TLS is
required, a reverse proxy or an SSH tunnel is used.

Daemon authentication (bcrypt, sessions, `X-API-Key`, rate limit) —
[api.md](api.md).

---

## Verifying the setup on a real drive

Installing the binary:

```bash
sha256sum -c lentovodets-v1.0.0-linux-amd64.sha256
install -m 0755 lentovodets-v1.0.0-linux-amd64 /usr/local/bin/lentovodec
```

Hardware tests (build tag `tape`; skipped when no device is set): raw
tape operations and an end-to-end scenario
format → backup → readtest → restore full with a byte-for-byte
comparison:

```bash
LENTOVODEC_TAPE_DEVICE=/dev/nst0 go test -tags tape ./test/hardware/...
```

Re-formatting an already formatted cartridge in the tests — via the
env `LENTOVODEC_TAPE_REFORMAT=1`.

A quick production check — `lentovodec tape readtest`: a diagnostic
read of the whole cartridge with hash verification, nothing written to
the FS.

---

## Troubleshooting (cartridge read/write and the systemd sandbox)

Typical first-deployment problems on real hardware. Common diagnostics
for the whole list:

```bash
mt -f /dev/nst0 status       # drive state (on Arch Linux — mt-st)
sudo dmesg | tail            # what the st driver said (Arch: dmesg is root-only)
sudo sg_logs -l /dev/nst0    # TapeAlert manually (sg3_utils package; see item 5)
journalctl -u lentovodec -e  # the daemon log
```

### 1. EIO on a "clean" cartridge: foreign markup

Symptom: `tape format` / `tape info` fail with a block read error —
`linuxtape: чтение блока: read /dev/nst0: input/output error`
(`чтение блока` = "block read"; or `unexpected EOF`) although the
cartridge looks blank. The cause: the
cartridge was previously written by other software (LTFS, bare
tar/dd) — the first block does not read as a label.

The fix — write one filemark at the beginning of the tape:

```bash
mt -f /dev/nst0 rewind && mt -f /dev/nst0 weof && mt -f /dev/nst0 offline
```

(`offline` = eject; insert the cartridge back.) After that, reading at
BOT yields EOF — the tape is "empty", `format` succeeds. What `weof`
does: writes one filemark at BOT; the old data is **not physically
erased**, but becomes unreachable once Lentovodets rewrites the
label/EOD. The preparation is needed only for cartridges with foreign
markup — not for new ones. The command writes to the tape → device
access is required (the `tape` group); on Arch Linux the utility is
called `mt-st` (package `mt-st`), on other distributions — `mt`.

### 2. EROFS writing the TOML (jobs add via the Web UI)

Symptom: `jobs add` through the Web UI fails with `read-only file
system`. The cause: `ProtectSystem=strict` +
`ReadWritePaths=/var/lib/lentovodec` — the `/etc/lentovodec` directory
is read-only for the daemon.

The fix: add `/etc/lentovodec` to the unit's `ReadWritePaths`:

```ini
ReadWritePaths=/var/lib/lentovodec /etc/lentovodec
```

```bash
systemctl daemon-reload && systemctl restart lentovodec
```

### 3. EACCES writing the TOML: config directory permissions

Symptom: right after item 2 — `permission denied`: the daemon cannot
create the temporary `lentovodec.toml.tmp-*.toml` (the config is
written atomically — assembled into a temp file next to it and then
renamed). The cause: `/etc/lentovodec` belongs to `root:root` with
`0755`.

The fix:

```bash
chown lentovodec:lentovodec /etc/lentovodec
chmod 0750 /etc/lentovodec
```

(an alternative: `root:lentovodec 0770`).

### 4. Restore into a directory outside the sandbox

Symptom: a restore into `/tank/...` (outside `/var/lib/lentovodec`)
fails with a write error in the destination directory; before session
17, Smart restore would report "all known copies damaged" at this
point, masking the cause. The error is the same sandbox:
`ProtectSystem=strict` closes the whole FS except `ReadWritePaths`.

The fix: add the destination directory to `ReadWritePaths` and grant
the daemon user access through Unix permissions:

```bash
setfacl -m u:lentovodec:rwx /tank/restore
```

The rule: **everything the daemon writes to — both in
`ReadWritePaths` and accessible to the user through Unix
permissions** — the systemd sandbox and FS permissions act
independently; neither one alone helps.

### 5. tapealert unavailable: SG_IO LOG SENSE

Symptom: `tape info` reads the label, but TapeAlert reports
"diagnostics unavailable"; the log shows `SG_IO LOG SENSE: operation
not permitted`. This is expected for rootless: since kernel ~5.19
passthrough SCSI commands are filtered by the kernel whitelist, and
LOG SENSE is not in it (it requires `CAP_SYS_RAWIO`). Functionality is
unaffected: tape reads/writes go through ordinary syscalls. Alerts
manually:

```bash
sudo sg_logs -l /dev/nst0    # sg3_utils package
```

### When the fix does not help

- **Cartridge/drive generation mismatch**: LTO drives read two
  generations back and write one (for example, an LTO-4 cartridge in
  an LTO-5 drive — readable and writable); a cartridge newer than the
  drive is not read at all.
- **Dirty heads**: read/write errors growing from cartridge to
  cartridge — a cleaning cartridge (the TapeAlert "cleaning required"
  flag in `sg_logs`, or `tape info` when diagnostics are available).
- **Worn media**: media wear / soft errors in LOG SENSE — try another
  cartridge; a full media check is `tape readtest`.

---

## Arch Linux specifics (first-install problems)

1. **The `storage` group instead of `tape` on `/dev/nst*`, and no
   `tape` group** — Arch's patch over the default systemd rules; the
   fix is §2.
2. **`nodejs` and `npm` are separate packages**
   (`sudo pacman -S nodejs npm`). Without npm, `make web-build` fails
   ("npm: command not found"), and `make build` after it — "pattern
   assets: no matching files found": the
   `internal/iface/web/assets/` directory does not exist in a fresh
   clone; it is created only by the Vite build (`make web-build` is
   required before `make build`).
3. **`mt` — the `mt-st` package, in the AUR only** (`yay -S mt-st`;
   the utility is not in the base repos), and the binary is called
   `mt-st`, not `mt`: `mt-st -f /dev/nst0 status`.
4. **`dmesg` is root-only**: `sudo dmesg | grep -i st`.
