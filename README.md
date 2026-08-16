# Lentovodets

Самописная система резервного копирования на ленточные стримеры LTO
(по умолчанию `/dev/nst0`), с каталогом в SQLite и двумя интерфейсами
управления: CLI и Web.

> Имя проекта — **Lentovodets**; имя модуля Go, бинаря и конфига —
> `lentovodec` (`lentovodec.toml`, env-префикс `LENTOVODEC_*`).

> **Статус:** реализованы все этапы 0–10 (domain → ports → адаптеры →
> use cases → CLI/Web → UI → интеграционные тесты → CI). История — в
> [docs/ROADMAP.md](docs/ROADMAP.md), изменения — в
> [docs/CHANGELOG.md](docs/CHANGELOG.md).

> Старая реализация сохранена как референс в `legacy/nil-backup/`
> (локально, в git не коммитится); из нового кода она не импортируется —
> см. [docs/LEGACY_REFERENCE.md](docs/LEGACY_REFERENCE.md).

## Возможности

- Инкрементальные бекапы по mtime+size+xxhash64; режимы `append`
  (дозапись версий) и `mirror` (tombstone'ы удалений, восстановление
  воссоздаёт актуальное состояние дерева).
- Собственный двоичный формат ленты ([docs/FORMAT.md](docs/FORMAT.md)):
  JSON-ярлык кассеты, JSON-индекс + tar-поток на сессию, filemark-инварианты,
  проверка xxhash при любом чтении.
- Каталог SQLite: кассеты, сессии, копии файлов; поиск, prune, smart-restore
  со свежей копии и fallback'ом на старые при порче.
- Rootless: стример управляется без root (группа `tape`), демон и CLI —
  один выделенный пользователь.
- Web UI (Vue 3, встроен в бинарь): лента, задания, каталог, файлы,
  восстановление, прогресс; REST API с bcrypt-логином, сессиями и
  rate-limit'ом.

## Требования

| Что                     | Зачем                                        |
| ----------------------- | -------------------------------------------- |
| Go 1.26+                | сборка (`go.mod` требует 1.26.1+)            |
| Node 22+ / npm          | сборка Web UI (`make web-build`, только для сборки) |
| Linux + `/dev/nst*`     | реальный стример (группа `tape`)             |
| Windows / macOS         | dev/CI: эмулятор ленты `filetape` (без тега `tape`) |

CGO не требуется (SQLite — `modernc.org/sqlite`), бинарь статический.

## Сборка и установка

Из исходников:

```bash
make web-build   # Web UI → internal/iface/web/assets (на fresh clone —
                 # обязательно ДО make build: бандл встраивается //go:embed)
make build       # бинарь bin/lentovodec
make build-tape  # то же + adapter/linuxtape (build tag `tape`, Linux)
```

Готовый бинарь — из CI: ручной прогон
[`Build and Test Lentovodets`](.forgejo/workflows/build.yml) с input
`push_to_registry=true` кладёт `lentovodets-<version>-linux-amd64`(+`.sha256`)
в [Packages](https://git.yadr00.internal/AlexRus1234/-/packages?q=lentovodets)
и в [Releases](https://git.yadr00.internal/AlexRus1234/Lentovodets/releases).
`<version>` — точный тег `v*` на HEAD, input `version_tag`, либо `sha-<hex8>`.

Проверка и установка:

```bash
sha256sum -c lentovodets-v1.0.0-linux-amd64.sha256
install -m 0755 lentovodets-v1.0.0-linux-amd64 /usr/local/bin/lentovodec
```

## Быстрый старт (CLI)

```bash
lentovodec jobs add media --paths /tank/data --mode mirror
lentovodec passwd           # bcrypt-хеш → web_password_hash в TOML
lentovodec tape format LTO-001
lentovodec backup media
lentovodec restore --paths /tank/data/a.txt --dest /safe
lentovodec daemon           # REST API + Web UI на 127.0.0.1:29201
```

## Конфигурация

`lentovodec.toml` (по умолчанию в рабочем каталоге; `--config PATH`):

```toml
db    = "lentovodec.db"
device = "/dev/nst0"
log   = "lentovodec.log"
server = "http://127.0.0.1:29201"
log_level = "info"           # debug | info | warn | error

# --- Web-доступ (демон) ---
bind = "127.0.0.1:29201"     # loopback = из сети не виден (SSH-туннель);
                             # LAN-адрес — осознанный выбор, потребует auth
web_username = "admin"       # одна учётка; пустая = auth выключен
                             # (разрешено ТОЛЬКО при bind на loopback)
web_password_hash = "$2a$..." # bcrypt; генерируется `lentovodec passwd`
api_key = ""                 # ключ для скриптов (X-API-Key); пустой — выключен
session_ttl = "72h"

[[jobs]]
Name = "media"
Description = "Бекап медиатеки"
Mode = "append"              # append | mirror
Paths = ["/tank/data/media"]
Exclude = ["**/.DS_Store", "**/*.partial"]
```

Слои: defaults → TOML → env (`LENTOVODEC_DEVICE`, `LENTOVODEC_DB`, …) →
флаги. Секреты (`web_password_hash`, `api_key`) через env не передаются —
только TOML с правами 0600.

## CLI

Глобальные флаги: `--config`, `--device`, `--db`, `--log`, `--server`,
`-v/--verbose`.

| Команда                                    | Режим       | Описание                                            |
| ------------------------------------------ | ----------- | --------------------------------------------------- |
| `lentovodec backup <job> [--full]`         | local       | Запустить задание                                   |
| `lentovodec restore [--paths p1,p2]`       | local       | `--paths` → smart; иначе full. `--dest`, `--original` |
| `lentovodec tape format <name> [--force]`  | local       | Форматировать ленту                                 |
| `lentovodec tape readtest`                 | local       | Диагностическое чтение                              |
| `lentovodec tape info`                     | daemon      | Прочитать ярлык                                     |
| `lentovodec tape eject`                    | daemon      | Извлечь ленту                                       |
| `lentovodec jobs list`                     | local       | Показать задания из TOML                            |
| `lentovodec jobs add <name>`               | local       | Добавить задание (`--paths`, `--mode`, `--desc`, `--exclude`) |
| `lentovodec jobs remove <name>`            | local       | Удалить задание                                     |
| `lentovodec catalog tapes`                 | daemon      | Список кассет                                       |
| `lentovodec catalog sessions [--tape U]`   | daemon      | Список сессий                                       |
| `lentovodec catalog files --session N`     | daemon      | Файлы сессии                                        |
| `lentovodec catalog search <pattern>`      | daemon      | Поиск файлов                                        |
| `lentovodec catalog rm --session N`        | daemon      | Удалить сессию из каталога                          |
| `lentovodec catalog prune --days N`        | daemon      | Удалить сессии старше N дней                        |
| `lentovodec passwd`                        | local       | bcrypt-хеш для `web_password_hash` (пароль дважды)  |
| `lentovodec daemon [--port 29201] [--bind 127.0.0.1]` | server | Запустить демона |

`local` — прямой доступ к ленте, демон не нужен. `daemon` — команда идёт в
HTTP API (`--server`): `api_key` из TOML, либо интерактивный логин и
Bearer-токен. Полная семантика — [SPECIFICATION §5](docs/SPECIFICATION.md).

## Web UI

- Прод: бандл встроен в бинарь (`//go:embed`), демон раздаёт его на
  `bind` (по умолчанию `http://127.0.0.1:29201`). Вход —
  `web_username`/`web_password_hash`; на loopback-бинде без учётки
  аутентификация выключена.
- Дев: `make web-dev` — Vite на `:5173` с прокси `/api` на демона.
- Удалённый доступ — SSH-туннель (по умолчанию демон из сети не виден):

  ```bash
  ssh -L 29201:127.0.0.1:29201 lentovodec@server
  # затем локально: http://localhost:29201
  ```

## Rootless-развёртывание

Root для стримера не нужен: `/dev/nst*` — character-устройство группы
`tape`, ioctl ленты не требуют `CAP_SYS_RAWIO`. Критерий доступа — успешное
открытие устройства (probe на старте), не `uid == 0`; при EACCES/ENOENT —
подсказка `usermod -aG tape <user>`. Root нужен только для
`--preserve-ownership` (chown) и путей, закрытых правами.

Демон и **все** local-команды должны работать от одного пользователя: у
SQLite в WAL рядом с БД лежат `db-wal`/`db-shm`.

### 1. Пользователь и группа

```bash
useradd --system --home-dir /var/lib/lentovodec --create-home \
        --shell /usr/sbin/nologin lentovodec
usermod -aG tape lentovodec     # группа вступает в силу в НОВОЙ сессии
```

Проверка (без релогина демона): `sudo -u lentovodec test -r /dev/nst0 && echo ok`.

### 2. udev

В дистрибутивах с `systemd` группа `tape` на `/dev/nst*` обычно уже
назначена; проверьте `ls -l /dev/nst0`. Если нет — правило
`/etc/udev/rules.d/88-tape.rules`:

```
KERNEL=="nst[0-9]*", GROUP="tape", MODE="0660"
```

и `udevadm control --reload && udevadm trigger`.

### 3. Конфиг и данные

```bash
install -d -m 0750 -o lentovodec -g lentovodec /var/lib/lentovodec
install -d -m 0755 /etc/lentovodec
install -m 0640 -o lentovodec -g lentovodec lentovodec.toml /etc/lentovodec/
```

В TOML: `db = "/var/lib/lentovodec/lentovodec.db"`,
`log = "/var/lib/lentovodec/lentovodec.log"`. Конфиг для демона —
только чтение (запись заданий `jobs add` выполняйте от того же
пользователя с правами записи в TOML).

### 4. systemd-юнит с харднингом

`/etc/systemd/system/lentovodec.service`:

```ini
[Unit]
Description=Lentovodets tape backup daemon
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
```

Graceful shutdown встроен: SIGTERM → HTTP Shutdown (5 с) + ожидание
активной ленточной задачи (до 30 с).

## CI

`.forgejo/workflows/build.yml` — ручной запуск (`workflow_dispatch`) в
fedora:44-контейнере:

1. сборка Web UI (Node 22) → статического бинаря (Go 1.26, CGO off,
   `-trimpath`, версия из тега `-v*` / input / `sha-<hex8>`);
2. `go vet`, `gofmt -l`, тесты (`-race` — опционально), отчёт покрытия
   (информационный);
3. при `push_to_registry=true` — бинарник + sha256 в Forgejo Packages
   (generic `lentovodets`) и в Release репозитория.

Hardware-тесты на реальном стримере в CI не гоняются: `test/hardware/`
(build tag `tape`) запускается вручную на машине с приводом —
`LENTOVODEC_TAPE_DEVICE=/dev/nst0 go test -tags tape ./test/hardware/...`.

## Разработка

| Команда          | Назначение                                        |
| ---------------- | ------------------------------------------------- |
| `make lint`      | `golangci-lint run ./...` (строгий конфиг)        |
| `make vet`       | `go vet ./...`                                    |
| `make test`      | `go test ./...`                                   |
| `make test-race` | `go test -race ./...`                             |
| `make cover`     | покрытие, итоговая строка                         |
| `make build`     | `go build -o bin/lentovodec ./cmd/lentovodec`     |
| `make build-tape`| сборка с тегом `tape` (включает `adapter/linuxtape`) |
| `make web-build` | Vite-сборка Vue UI в `internal/iface/web/assets/` |
| `make web-dev`   | dev-сервер Vite с прокси на `:29201`              |
| `make clean`     | удалить `bin/`, `coverage/`, web-бандл            |

Coverage-цели — [docs/TESTING.md §4](docs/TESTING.md); фактические
значения по этапам — заметки в [docs/ROADMAP.md](docs/ROADMAP.md).

Главное архитектурное правило:

> **Бизнес-логика (`internal/domain`, `internal/usecase`) не имеет права
> импортировать `os`, `syscall`, `net` и любые конкретные адаптеры.** Весь
> ввод-вывод — через интерфейсы из `internal/port`. Проверяется линтером
> `depguard`. Время — только через `port.Clock`, случайность — через
> `port.Rand`.

Перед правками читайте документацию (порядок для нового исполнителя):

1. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — слои, правила импортов.
2. [docs/SPECIFICATION.md](docs/SPECIFICATION.md) — требования, CLI, REST API, схема БД.
3. [docs/FORMAT.md](docs/FORMAT.md) — двоичный формат ленты.
4. [docs/ROADMAP.md](docs/ROADMAP.md) — план и история этапов.
5. [docs/TESTING.md](docs/TESTING.md) — стратегия тестирования.
6. [docs/LEGACY_REFERENCE.md](docs/LEGACY_REFERENCE.md) — что брать из `legacy/`.

## Зафиксированные технологические решения

| Параметр             | Значение                                  |
| -------------------- | ----------------------------------------- |
| Имя проекта          | Lentovodets                                |
| Имя модуля и бинаря  | `lentovodec`                               |
| Язык                 | Go 1.26+ (без CGO)                         |
| Архитектура          | Hexagonal (Ports & Adapters)               |
| Логирование          | `log/slog` (стандартная библиотека)        |
| Линтер               | `golangci-lint` (строгая конфигурация)     |
| База данных          | SQLite через `modernc.org/sqlite`          |
| HTTP-роутер          | `go-chi/chi/v5`                            |
| CLI                  | `spf13/cobra` + `spf13/viper`              |
| Web UI               | Vue 3 + Vite, бандлится в `embed.FS`       |
| Web-доступ           | bcrypt + сессии в памяти; bind по умолчанию loopback:29201 |
| Формат ленты         | новый, старые кассеты legacy не читаются   |
