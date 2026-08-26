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

# Lentovodets Documentation

This directory contains Lentovodets functional documentation in English.
The root `README.en.md` provides a system overview and initial setup
instructions; details requiring separate treatment are documented here.

| File | Contents |
|---|---|
| [features.md](features.md) | Features: jobs and backup modes, restore, catalog, tape, daemon, Web UI, and security. |
| [cli.md](cli.md) | CLI reference: global flags, local/daemon commands, device selection, examples. |
| [api.md](api.md) | Daemon REST API: authentication, endpoints, background tasks and progress. |
| [os-setup.md](os-setup.md) | Linux deployment: the rootless model, the `tape` group, udev, the systemd unit, network access, troubleshooting (foreign cartridges, the systemd sandbox, TapeAlert). |
| [tape-format.md](tape-format.md) | The tape format from the operator's point of view: the label, sessions, filemarks, compatibility. |

The Russian documentation is in [`docs/func/ru/`](../ru/README.md).

Internal development documentation is in [`docs/`](../../):
`ARCHITECTURE.md` (layers and import rules), `SPECIFICATION.md` (full
requirements and schemas), `FORMAT.md` (the byte canon of the format),
`TESTING.md` (testing strategy), `ROADMAP.md` (stage history).
