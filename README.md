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

**Русский**

<div align="center">

<h1>Лентоводец</h1>

**Система резервного копирования на ленточные накопители LTO**

Лентоводец представляет собой автономную систему резервного копирования
на ленточные стримеры LTO. CLI, HTTP-демон с Web UI и кодек ленты
объединены в одном исполняемом файле; метаданные всех кассет, сессий и
файлов хранятся в локальном каталоге SQLite. Внешняя СУБД и контейнерная
инфраструктура не требуются, root-права не нужны.

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue.svg?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg?style=flat-square)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D.svg?style=flat-square)](https://vuejs.org/)
[![Vite](https://img.shields.io/badge/Vite-7-646CFF.svg?style=flat-square)](https://vite.dev/)
[![Platform](https://img.shields.io/badge/Linux-any-1793D1.svg?style=flat-square)](#быстрый-старт)

</div>

---

## Содержание

- [Возможности](#возможности)
- [Быстрый старт](#быстрый-старт)
- [Конфигурация](#конфигурация)
- [Права доступа](#права-доступа)
- [CLI, API и Web UI](#cli-api-и-web-ui)
- [Развёртывание](#развёртывание)
- [Структура проекта](#структура-проекта)
- [Технологический стек](#технологический-стек)
- [Разработка](#разработка)
- [Лицензия](#лицензия)

> Расширенная документация по возможностям, CLI, REST API, формату ленты
> и развёртыванию приведена в каталоге [`docs/func/ru/`](docs/func/ru/README.md).
> Настоящий файл содержит обзор системы и инструкцию по первоначальному запуску.

Проект разработан в соответствии с заранее определённой архитектурой; при
подготовке исходного кода использовался ИИ-ассистент.[^1]

---

## Возможности

### Резервное копирование

- Инкрементальные бекапы: новые и изменённые файлы определяются по
  размеру, mtime и xxhash64
- Многотомные бекапи с продолжением по кассетам (spanning): сессия режется
  по границам файлов, каждая кассета читается голым GNU tar (рецепт DR —
  `docs/func/ru/tape-format.md`)
- Режимы заданий `append` (версионная дозапись) и `mirror` (tombstone'ы
  удалений — восстановление реконструирует зеркало каталога на момент
  любой сессии)
- Полные (`--full`) и инкрементальные сессии; первая сессия кассеты всегда
  FULL; `--dry-run` для пробного прогона
- Exclude-фильтры: glob и doublestar (`**/.DS_Store`, `.git/**`,
  `node_modules`)
- Статистика сессии (просмотрено/добавлено/изменено/удалено/байты) и
  прогресс в реальном времени (текущий файл, %, скорость)

### Восстановление

- **Full** — вся кассета подряд; повреждённые сессии пропускаются с
  переходом к следующим
- **Selective** — выбранные пути из конкретной сессии (Web UI)
- **Smart** — по путям через каталог: берётся свежая копия, при порче
  блока выполняется fallback на более старые; ни одной здоровой копии →
  понятная ошибка
- xxhash64 сверяется при любом восстановлении
- Восстановление в безопасный каталог (`--dest`) или по исходным путям
  (`--original`)

### Каталог

- SQLite (WAL): кассеты, сессии, копии файлов всех сессий
- Поиск по подстроке, фильтрация по кассете, удаление сессий, prune
  устаревших
- Smart-restore опирается на каталог и умеет переживать физическую порчу
  отдельной копии файла

### Лента

- Собственный формат `LENTOVODEC_TAPE_V2`: JSON-ярлык кассеты, сессии
  «JSON-индекс + tar-поток» с filemark-инвариантами (детали —
  [`docs/func/ru/tape-format.md`](docs/func/ru/tape-format.md))
- `format` (UUID и имя кассеты), `readtest` (диагностическое чтение со
  сверкой хешей без записи на ФС), `eject`, `info`
- Кассеты legacy `nil-backup` намеренно не читаются
- Эмулятор `filetape`: обычный файл ведёт себя как лента — разработка и
  CI на любой ОС без стримера

### Интерфейсы и безопасность

- CLI: local-команды работают с лентой напрямую, daemon-команды — через
  HTTP API
- Демон: фоновые задачи бекапа/восстановления (одна активная — стример
  один), REST API, graceful shutdown
- Web UI (Vue 3, встроен в бинарь): лента, задания, каталог, файлы,
  восстановление; русский и английский языки
- Аутентификация: логин/пароль (bcrypt) + Bearer-сессии в памяти,
  `X-API-Key` для скриптов, rate-limit на логин
- Rootless: стример управляется без root и без `CAP_SYS_RAWIO` (группа
  `tape`); демон по умолчанию слушает только loopback

Подробное описание приведено в [`docs/func/ru/features.md`](docs/func/ru/features.md).

---

## Быстрый старт

### Требования

| Компонент | Версия | Назначение |
|---|---|---|
| **Go** | 1.26+ | Сборка бинарника |
| **Node.js** | 22+ | Сборка Web UI |
| **Linux + LTO-стример** | `/dev/nst*` | Рабочее окружение |
| Windows / macOS | — | Разработка без стримера (эмулятор `filetape`) |

### Сборка

```bash
# Сборка Web UI и серверной части в порядке, используемом CI:
make web-build
make build        # бинарь bin/lentovodec (без драйвера стримера)
make build-tape   # то же + adapter/linuxtape (build tag `tape`, Linux)
```

CGO не требуется (SQLite — `modernc.org/sqlite`), бинарь статический.

Сборка для рабочей среды (как в CI):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -tags tape -trimpath -ldflags="-s -w \
  -X lentovodec/cmd/lentovodec.Version=1.0.0" \
  -o lentovodec ./cmd/lentovodec
```

> Релизные бинарники публикуются вручную из CI в Packages и Releases
> Forgejo (`lentovodets-<version>-linux-amd64` + `.sha256`); `<version>` —
> точный тег `v*` на HEAD, input `version_tag` либо `sha-<hex8>`. Для
> разработки сборка выполняется из исходного кода.

### Запуск

```bash
# 1. Задание бекапа (запись в lentovodec.toml):
lentovodec jobs add media --paths /tank/data/media --mode mirror \
    --exclude "**/.DS_Store,**/*.partial"

# 2. Кассета. Устройство задаётся --device (по умолчанию /dev/nst0);
#    обычный файл работает как эмулятор ленты (dev/test):
lentovodec --device /tmp/tape.img tape format LTO-001
lentovodec --device /tmp/tape.img backup media

# 3. Восстановление:
lentovodec --device /tmp/tape.img restore \
    --paths /tank/data/media/movie.mkv --dest /safe

# 4. Демон: REST API + Web UI на http://127.0.0.1:29201
lentovodec passwd     # bcrypt-хеш → web_password_hash в TOML
lentovodec daemon
```

---

## Конфигурация

`lentovodec.toml` (по умолчанию в рабочем каталоге; глобальный флаг
`--config`):

```toml
db    = "lentovodec.db"
device = "/dev/nst0"
log   = "lentovodec.log"
server = "http://127.0.0.1:29201"
log_level = "info"           # debug | info | warn | error

# --- Web-доступ (демон) ---
bind = "127.0.0.1:29201"     # loopback = из сети не виден (SSH-туннель);
                             # LAN-адрес — осознанный выбор, потребует пароль
web_username = "admin"       # учётка ровно одна; пустая строка = auth выключен
                             # (разрешено ТОЛЬКО при bind на loopback)
web_password_hash = "$2a$..." # bcrypt; генерируется `lentovodec passwd`
api_key = ""                 # ключ для скриптов (X-API-Key); пустой — отключён
session_ttl = "72h"

# --- Ёмкость кассеты (планировщик частей) ---
capacity = "2.2T"            # оценка ёмкости кассеты (LTO-6 — с запасом);
                              # отсутствие/0 — поведение одной кассеты
min_tail = "100G"            # остаток кассеты меньше порога — новая сессия
                              # начинается на новой кассете; дефолт — 5% capacity

[[jobs]]
Name = "media"
Description = "Бекап медиатеки"
Mode = "append"              # append | mirror
Paths = ["/tank/data/media"]
Exclude = ["**/.DS_Store", "**/*.partial"]
```

Слои применения: defaults → TOML → env (`LENTOVODEC_DEVICE`,
`LENTOVODEC_DB`, …) → флаги CLI. Секреты (`web_password_hash`, `api_key`)
через env не передаются — только TOML с правами 0600.

---

## Права доступа

Управление стримером **не требует root**: `/dev/nst*` — обычное
character-устройство (группа `tape`), ioctl ленты не требуют
`CAP_SYS_RAWIO`. Критерий доступа — успешное открытие устройства (probe
на старте команд и демона), а не `uid == 0`; при `EACCES`/`ENOENT` —
понятная ошибка с подсказкой (`usermod -aG tape <user>`).

Демон и все local-команды должны работать от **одного** пользователя: у
SQLite в WAL-режиме рядом с БД лежат `db-wal`/`db-shm`.

```bash
useradd --system --home-dir /var/lib/lentovodec --create-home \
        --shell /usr/sbin/nologin lentovodec
usermod -aG tape lentovodec
```

Полное руководство (udev, каталоги данных, systemd-юнит с харднингом,
проверка стенда на реальном стримере) — в
[`docs/func/ru/os-setup.md`](docs/func/ru/os-setup.md).

---

## CLI, API и Web UI

| Интерфейс | Кратко | Подробности |
|---|---|---|
| **CLI** | `backup`, `restore`, `tape`, `jobs`, `catalog`, `passwd`, `daemon`; local-команды — прямой доступ к ленте, daemon-команды — HTTP | [`docs/func/ru/cli.md`](docs/func/ru/cli.md) |
| **REST API** | `/api/*`: аутентификация, лента, задания, фоновые задачи, каталог | [`docs/func/ru/api.md`](docs/func/ru/api.md) |
| **Web UI** | Экраны Login, Tape, Jobs, Catalog, Files; RU/EN; встроен в бинарь (`go:embed`) | [`docs/func/ru/api.md`](docs/func/ru/api.md) |

Демон — «инструмент одной машины»: по умолчанию слушает `127.0.0.1:29201`
и из сети не виден. Управление с другой машины — через SSH-туннель:

```bash
ssh -L 29201:127.0.0.1:29201 lentovodec@server
# затем на ноутбуке: http://localhost:29201
```

Прямой доступ из LAN — осознанный выбор оператора (меняется `bind`); при
этом демон требует настроенный пароль и без него отказывается стартовать.

---

## Развёртывание

Типовое развёртывание в Linux — выделенный пользователь `lentovodec`
(группа `tape`), данные в `/var/lib/lentovodec`, конфиг в
`/etc/lentovodec`, systemd-юнит с харднингом (`ProtectSystem=strict`,
`SystemCallFilter=@system-service` и др.).

Пошаговая инструкция — в [`docs/func/ru/os-setup.md`](docs/func/ru/os-setup.md).

---

## Структура проекта

```
.
├── cmd/lentovodec/          # Точка входа: wiring (~50 строк)
├── internal/
│   ├── domain/              # Чистые типы: Job, FileMeta, Session, TapeLabel, ошибки
│   ├── port/                # Интерфейсы: Tape, Filesystem, Catalog, Clock, Rand, …
│   ├── usecase/             # Оркестрация: scan, backup, restore, format, catalog
│   ├── adapter/             # Реализации портов: tapeformat, linuxtape, filetape,
│   │                        # osfs, sqlite, tomlconfig, xxhash, sloglog
│   ├── iface/               # Доставка: cli (cobra), web (chi + REST + embed), destfs
│   └── testutil/            # Общие test doubles (FakeTape, MapFS, MemCatalog, …)
├── test/
│   ├── integration/         # Сквозные сценарии на реальных адаптерах (без железа)
│   └── hardware/            # //go:build tape — прогон на реальном стримере
├── web/                     # Исходники Vue 3 + Vite (бандл → iface/web/assets)
├── docs/                    # func/ru/ — пользовательская документация;
│                            # ARCHITECTURE/SPECIFICATION/FORMAT/… — для разработки
├── .forgejo/workflows/      # CI: сборка, тесты, публикация релиза
├── LICENSE                  # GNU GPL v3
└── README.md                # Основная документация
```

Бизнес-логика (`domain`, `usecase`) не импортирует `os`, `syscall`, `net`
и конкретные адаптеры — весь ввод-вывод через интерфейсы `internal/port`;
правило проверяется линтером `depguard`.

---

## Технологический стек

**Бэкенд:** Go 1.26 · cobra / viper · chi v5 · modernc.org/sqlite (без
CGO) · cespare/xxhash/v2 · golang.org/x/crypto (bcrypt) ·
bmatcuk/doublestar/v4 · `log/slog` · `go:embed`.

**Фронтенд:** Vue 3 (Composition API) · Vite 7 · TypeScript 5.9 (vue-tsc).

**Инфраструктура и качество:** Forgejo Actions (CI) · golangci-lint
(строгий конфиг, depguard) · `go vet` / `gofmt` · `go test` (включая
`-race`) · unit- / integration- / hardware-уровни тестов.

---

## Разработка

| Команда | Назначение |
|---|---|
| `make lint` | `golangci-lint run ./...` (строгий конфиг) |
| `make vet` | `go vet ./...` |
| `make test` | `go test ./...` |
| `make test-race` | `go test -race ./...` |
| `make cover` | Отчёт о покрытии |
| `make build` | Сборка `bin/lentovodec` |
| `make build-tape` | Сборка с тегом `tape` (драйвер реального стримера) |
| `make web-build` | Vite-сборка Web UI в `internal/iface/web/assets/` |
| `make web-dev` | Dev-сервер Vite с прокси `/api` на `:29201` |
| `make clean` | Удалить `bin/`, `coverage/`, web-бандл |

Fresh clone: сначала `make web-build` (бандл встраивается через
`//go:embed`), затем `make build`.

Документация для разработчиков (порядок чтения перед правками):

1. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — слои, правила импортов.
2. [docs/SPECIFICATION.md](docs/SPECIFICATION.md) — требования, CLI, REST API, схема БД.
3. [docs/FORMAT.md](docs/FORMAT.md) — канон двоичного формата ленты.
4. [docs/TESTING.md](docs/TESTING.md) — стратегия тестирования.
5. [docs/ROADMAP.md](docs/ROADMAP.md) — план и история этапов.
6. [docs/CHANGELOG.md](docs/CHANGELOG.md) — изменения между релизами.

---

## Лицензия

Проект распространяется под лицензией **[GNU General Public License v3.0](LICENSE)**.

```
Лентоводец — система резервного копирования на ленточные накопители LTO
Copyright (C) 2026  AlexRus1234

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
```

[^1]: Разработка исходного кода выполнялась с использованием ИИ-ассистента в
соответствии с заранее определённой архитектурой проекта; архитектурные решения,
проверка результатов и итоговая интеграция осуществлялись автором.
