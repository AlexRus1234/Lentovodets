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

Группа `tape` в Debian/Fedora есть из коробки, в Arch Linux — нет
(см. §2); создаём при отсутствии:

```bash
getent group tape || groupadd -r tape
useradd --system --home-dir /var/lib/lentovodec --create-home \
        --shell /usr/sbin/nologin lentovodec
usermod -aG tape lentovodec     # группа вступает в силу в НОВОЙ сессии
```

Проверка (без релогина демона):

```bash
sudo -u lentovodec test -r /dev/nst0 && echo ok
```

---

## 2. udev и группа устройства

Дефолтная группа на `/dev/nst*` зависит от дистрибутива:

| Дистрибутив                              | `/dev/nst*` по умолчанию | Группа `tape` |
| ---------------------------------------- | ------------------------ | ------------- |
| Debian, Fedora (апстримный systemd)      | `root:tape` 0660         | есть          |
| Arch Linux                               | `root:storage` 0660      | **нет**       |

Arch патчит дефолтные udev-правила systemd
(`0001-Use-Arch-Linux-device-access-groups.patch`): ленточные ноды
достаются legacy-группе `storage`, а группы `tape` в поставке нет вообще.
Типовые грабли первой установки на Arch: демон стартует, но probe падает
с `permission denied`; `ls -l /dev/nst0` показывает `root storage`, а
`usermod -aG tape` либо отказывает (группы нет), либо бесполезен.

Диагноз:

```bash
ls -l /dev/st0 /dev/nst0     # root:storage → случай Arch
getent group tape            # пусто → группы нет
```

Решение — привести устройство к конвенции проекта (группа `tape`
совпадает с подсказками ошибок демона и юнитом):

```bash
getent group tape || sudo groupadd -r tape
```

`/etc/udev/rules.d/88-tape.rules`:

```
KERNEL=="st[0-9]*|nst[0-9]*", GROUP="tape", MODE="0660"
```

и примените:

```bash
sudo udevadm control --reload
sudo udevadm trigger -w /dev/st0 /dev/nst0
```

`-w` (ждать обработки события) важен при проверке: без него `trigger`
асинхронен, и `ls -l` сразу после команды покажет ещё старую группу —
выглядит как «правило не сработало», хотя оно применилось мгновениями
позже.

Альтернатива — не заводить `tape` и оставить арховскую `storage`
(`SupplementaryGroups=storage` в юните). Работает, но подсказки демона
(`usermod -aG tape ...`) перестают соответствовать реальности.

Смена группы на устройстве подхватывается демоном рестартом
(`systemctl restart lentovodec`): supplementary-группы процесса
фиксируются при его старте.

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
webhook_url = "https://ntfy.sh/my-private-topic"
webhook_timeout = "10s"
```

Конфиг для демона — только чтение (запись заданий `jobs add` выполняйте
от того же пользователя с правами записи в TOML). Секреты
(`web_password_hash`, `api_key`) — только TOML с правами 0600, через env
они не передаются.

### Webhook о завершении задач

После завершения backup или restore демон отправляет `POST` с JSON. Например:

```json
{"event":"task_finished","task_id":"task-ab12cd34","kind":"backup","state":"success","error":"","job":"media","bytes":123,"files":10,"tapes":["LTO-001"],"started_at":"2026-08-21T03:00:00Z","finished_at":"2026-08-21T03:12:00Z","version":"1.x"}
```

Для ntfy достаточно URL топика:

```bash
curl -X POST https://ntfy.sh/my-private-topic \
  -H 'Content-Type: application/json' \
  -d '{"event":"task_finished","state":"success"}'
```

Секреты v1 в webhook не передаются: токен может быть частью URL. Храните TOML
с правами `0600`. При сетевой ошибке или ответе не-2xx демон делает один
повтор, затем пишет предупреждение; результат задачи не меняется.

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

---

## Особенности Arch Linux (грабли первой установки)

1. **Группа `storage` вместо `tape` на `/dev/nst*`, группы `tape` нет** —
   патч Arch над дефолтными правилами systemd; лечение — §2.
2. **`nodejs` и `npm` — отдельные пакеты** (`sudo pacman -S nodejs npm`).
   Без npm падает `make web-build` («npm: command not found»), а следом
   `make build` — «pattern assets: no matching files found»: каталог
   `internal/iface/web/assets/` в свежем клоне не существует, его создаёт
   только Vite-сборка (`make web-build` обязателен до `make build`).
3. **`mt` — пакет `mt-st`**: в базовой поставке утилиты нет
   (`mt: command not found`).
4. **`dmesg` читается только root**: `sudo dmesg | grep -i st`.
