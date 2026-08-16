<!--
Лентоводец — система резервного копирования на ленточные накопители LTO
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

# Развёртывание в Linux

Лентоводец управляет ленточным стримером через `/dev/nst*` и ведёт
SQLite-каталог на диске. Развёртывание сводится к трём решениям: пользователь
процесса, права на устройство и способ автозапуска.

---

## Rootless-модель

Управление стримером **не требует root**:

- `/dev/nst*` — обычное character-устройство (группа `tape`); ioctl ленты
  не требуют `CAP_SYS_RAWIO`.
- **Критерий доступа** — успешное открытие устройства (probe на старте
  демона и local-команд), а не `uid == 0`. При отказе (`EACCES`/`ENOENT`)
  — понятная ошибка с подсказкой (`usermod -aG tape <user>` или
  udev-правило/ACL), а не предупреждение «запущен не от root».
- Привилегированный helper (polkit/udisks-style) рассмотрен и отвергнут:
  одна машина, один стример.

Root может понадобиться только для путей ФС, закрытых правами для
данного пользователя (например, бекап `/etc` целиком), — это ограничение
ФС, а не стримера.

**Важно:** демон и **все** local-команды должны работать от одного
пользователя: у SQLite в WAL-режиме рядом с БД лежат `db-wal`/`db-shm`,
смешивание пользователей потребовало бы общего group-writable каталога.

---

## 1. Пользователь и группа

```bash
useradd --system --home-dir /var/lib/lentovodec --create-home \
        --shell /usr/sbin/nologin lentovodec
usermod -aG tape lentovodec     # группа вступает в силу в НОВОЙ сессии
```

Проверка (без релогина демона):

```bash
sudo -u lentovodec test -r /dev/nst0 && echo ok
```

---

## 2. udev

В дистрибутивах с systemd группа `tape` на `/dev/nst*` обычно уже
назначена; проверьте `ls -l /dev/nst0`. Если нет — правило
`/etc/udev/rules.d/88-tape.rules`:

```
KERNEL=="nst[0-9]*", GROUP="tape", MODE="0660"
```

и примените:

```bash
udevadm control --reload && udevadm trigger
```

---

## 3. Конфиг и данные

```bash
install -d -m 0750 -o lentovodec -g lentovodec /var/lib/lentovodec
install -d -m 0755 /etc/lentovodec
install -m 0640 -o lentovodec -g lentovodec lentovodec.toml /etc/lentovodec/
```

В TOML:

```toml
db    = "/var/lib/lentovodec/lentovodec.db"
log   = "/var/lib/lentovodec/lentovodec.log"
device = "/dev/nst0"
```

Конфиг для демона — только чтение (запись заданий `jobs add` выполняйте
от того же пользователя с правами записи в TOML). Секреты
(`web_password_hash`, `api_key`) — только TOML с правами 0600, через env
они не передаются.

---

## 4. systemd-юнит

`/etc/systemd/system/lentovodec.service`:

```ini
[Unit]
Description=Лентоводец — демон ленточного бекапа
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
PrivateTmp=true
# PrivateDevices НЕ включать: нужен доступ к /dev/nst0
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

Graceful shutdown встроен: SIGTERM → остановка HTTP (5 с) → ожидание
активной ленточной задачи (до 30 с).

---

## Сетевой доступ

Демон — «инструмент одной машины»; по умолчанию `bind = 127.0.0.1:29201`,
из сети порт не виден.

**Удалённое управление** — через SSH-туннель (аутентификация и шифрование
достаются от SSH):

```bash
ssh -L 29201:127.0.0.1:29201 lentovodec@server
# затем на локальной машине: http://localhost:29201
```

**Прямой доступ из LAN** — осознанный выбор оператора: меняется `bind`
(например, `192.168.1.10:29201` или `0.0.0.0:29201`, в т.ч. флагами
`--bind`/`--port`). При этом демон **требует** настроенную
аутентификацию: не-loopback bind без `web_password_hash` и
`web_username` — отказ старта с понятной ошибкой. HTTP без TLS —
принятый компромисс homelab; нужен TLS — реверс-прокси спереди или
SSH-туннель.

Аутентификация демона (bcrypt, сессии, `X-API-Key`, rate-limit) —
[api.md](api.md).

---

## Проверка стенда на реальном стримере

Установка бинаря:

```bash
sha256sum -c lentovodets-v1.0.0-linux-amd64.sha256
install -m 0755 lentovodets-v1.0.0-linux-amd64 /usr/local/bin/lentovodec
```

Аппаратные тесты (build tag `tape`; скипаются, если устройство не
задано): сырые операции ленты и сквозной сценарий
format → backup → readtest → restore full с побайтовым сравнением:

```bash
LENTOVODEC_TAPE_DEVICE=/dev/nst0 go test -tags tape ./test/hardware/...
```

Повторное форматирование уже размеченной кассеты в тестах — через env
`LENTOVODEC_TAPE_REFORMAT=1`.

Быстрая проверка в эксплуатации — `lentovodec tape readtest`:
диагностическое чтение всей кассеты со сверкой хешей без записи на ФС.
