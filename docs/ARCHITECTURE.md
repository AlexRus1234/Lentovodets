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

# Архитектура

Этот документ — канон. Любой код, противоречащий ему, считается ошибкой.

## 1. Цели

| Цель                       | Подход                                                            |
| -------------------------- | ----------------------------------------------------------------- |
| Тестируемость 95%+         | Слои изолированы; всё железо за интерфейсами                      |
| Изоляция от среды          | `domain`/`usecase` не импортируют `os`/`syscall`                  |
| Возможность замены подсистем | Tape, FS, Catalog, Config — за портами; по тест-двойнику на каждый |
| Детерминированность        | Никаких глобалов, время за интерфейсом `Clock`, случайность за `Rand` |
| Возможность отмены         | `context.Context` во всех use case-методах                        |
| CI-ready                   | `golangci-lint`, `go test -race`, проверка coverage               |

## 2. Слои (Ports & Adapters)

```
                         ┌──────────────────────────────────────┐
                         │            interfaces                │
                         │   iface/cli   iface/web              │
                         └────────────────┬─────────────────────┘
                                          │ использует
                                          ▼
                         ┌──────────────────────────────────────┐
                         │              usecase                 │
                         │  backup / restore / format /         │
                         │  scan / catalog                     │
                         └────────────────┬─────────────────────┘
                                          │ зависит только от
                                          ▼
                          ┌──────────────────────────────────────┐
                          │     port (интерфейсы) + domain       │
                          │  Tape, TapeChanger, Filesystem,      │
                          │  Catalog, Progress, Clock, Config    │
                          └────────────────┬─────────────────────┘
                                          ▲ реализует
                                          │
                         ┌────────────────┴─────────────────────┐
                         │              adapter                 │
                         │  linuxtape, filetape, tapeformat,    │
                         │  osfs, sqlite, tomlconfig            │
                         └──────────────────────────────────────┘
```

**Правило зависимости:** стрелки импортов идут только внутрь.
`adapter` → `port`/`domain`; `usecase` → `port`/`domain`;
`iface` → `usecase`/`port`/`domain`. Никаких обратных импортов.

| Слой       | Зависит от                | Тестируется                       |
| ---------- | ------------------------- | --------------------------------- |
| `domain`   | только стандартная библиотека | unit, 100%                    |
| `port`     | `domain`                  | не тестируется (только интерфейсы)|
| `usecase`  | `port`, `domain`          | unit с фейками, 95%+              |
| `adapter`  | `port`, `domain`, внешние библиотеки | unit + integration     |
| `iface`    | `usecase`, `port`, `domain` | smoke + handler-тесты, 60-70%   |

## 3. Структура каталогов

```
lentovodec/
├── cmd/
│   └── lentovodec/            # package main; только wiring (DI-композиция)
│       └── main.go            # ~50 строк
├── internal/
│   ├── domain/                # ЧИСТЫЕ типы и функции, 0 внешних I/O
│   │   ├── job.go             # Job, JobMode
│   │   ├── filemeta.go        # FileMeta, FileState, переходы состояний
│   │   ├── session.go         # Session, SessionType
│   │   ├── tape.go            # TapeLabel, TapeInfo, константы формата
│   │   ├── filter.go          # glob-исключения, нормализация путей
│   │   └── errors.go          # ErrTapeFull, ErrLabelMismatch, ...
│   ├── port/                  # ИНТЕРФЕЙСЫ, описанные в терминах domain
│   │   ├── tape.go            # Tape: блоки + filemark'и + перемотки
│   │   ├── tapechanger.go     # TapeChanger: смена кассет spanning
│   │   ├── tapecodec.go       # TapeCodec: ярлык + сессии + указатели
│   │   ├── filesystem.go      # Walker, FileReader, FileWriter, Stater
│   │   ├── catalog.go         # CRUD над tapes/sessions/files
│   │   ├── hasher.go          # xxhash64 файлов
│   │   ├── progress.go        # ProgressReporter (не чаще 2 Гц)
│   │   ├── clock.go           # Clock (тестируемое время)
│   │   ├── rand.go            # Rand (тестируемая случайность)
│   │   └── config.go          # ConfigSource
│   ├── usecase/               # ОРКЕСТРАЦИЯ, зависит только от port/domain
│   │   ├── scan/              # FS + catalog → []FileMeta со State
│   │   ├── backup/            # BackupUseCase
│   │   ├── restore/           # RestoreUseCase: full/selective/smart
│   │   ├── format/            # FormatTapeUseCase
│   │   └── catalog/           # CatalogUseCase: list/search/prune/delete
│   ├── adapter/               # РЕАЛИЗАЦИИ port
│   │   ├── tapeformat/        # чистый stream+block format (no I/O)
│   │   ├── linuxtape/         # /dev/nst0, //go:build tape
│   │   ├── filetape/          # "лента-как-файл" для dev/CI
│   │   ├── osfs/              # Реальная ФС через os.*
│   │   ├── sqlite/            # SQLite-каталог (modernc.org/sqlite)
│   │   ├── tomlconfig/        # viper-based конфиг
│   │   ├── xxhash/            # хеширование файлов
│   │   └── sloglog/           # обёртка над slog
│   ├── iface/                 # ТОНКИЕ слои доставки
│   │   ├── cli/               # cobra-команды
│   │   └── web/               # chi-роутер + REST + embed
│   └── testutil/              # общие test doubles (FakeTape, memCatalog)
├── test/
│   ├── integration/           # FS + filetape + sqlite (без железа)
│   └── hardware/              # //go:build tape
├── web/                       # исходники Vue 3 + Vite
│   ├── src/
│   ├── package.json
│   └── vite.config.ts
├── docs/                      # этот каталог
├── .gitignore
├── .editorconfig
├── .golangci.yaml
├── Makefile
├── go.mod
└── README.md
```

`internal/` физически запрещает импорт снаружи — это первая линия защиты
границ. Дополнительная — `depguard` в `.golangci.yaml` (правила в
`docs/ROADMAP.md`, Этап 0).

## 4. Поток управления

### 4.1. `BackupUseCase.Backup(ctx, jobName, opts)`

```
                 ┌──────────────────────────────────────────────────┐
                 │  1. load Job по имени (Config)                  │
                 │  2. прочитать TapeLabel, взять tapeUUID         │
                 │  3. sessionNum = catalog.LastSessionNum(tapeUUID)+1 │
                 │  4. isFull = opts.Full || sessionNum == 1       │
                 └────────────────────────┬─────────────────────────┘
                                          ▼
                 ┌──────────────────────────────────────────────────┐
                 │  5. Scanner.Scan(paths, excludes, mode,         │
                 │                  catalog) → []FileMeta          │
                 │     • mirror: получить прошлый снимок, вычислить │
                 │       Added/Modified/Deleted                    │
                 │     • append: только Added/Modified             │
                 └────────────────────────┬─────────────────────────┘
                                          ▼
                  ┌──────────────────────────────────────────────────┐
                  │  6. sessionID = catalog.CreateSession(...)      │
                  │  7. планировщик частей (capacity/min_tail,      │
                  │     domain.PlanSpan): части по границам файлов  │
                  │  8. если isFull: Tape.Rewind                    │
                  │     иначе:      Tape.LocateEOD                  │
                  │  9. tapeformat.WriteSession(tape, files, FS,    │
                  │                              Progress)          │
                  │       • индекс + EOF                            │
                  │       • tar-поток + EOF                         │
                  │ 10. catalog.SaveFiles(sessionID, files)         │
                  │ 11. return                                      │
                  └──────────────────────────────────────────────────┘
```

Каждый шаг использует интерфейс, поэтому тесты подменяют любой из них.

**Ветка частей (spanning, Этап 11).** Частей >1 или остаток кассеты меньше
`min_tail` — шаги 7–10 выполняет цикл по кассетам (`usecase/backup/span.go`):
часть пишется на текущую ленту, незаключительная часть закрывается
блоком-указателем продолжения (FORMAT §5.1), смена кассеты — через порт
`TapeChanger` (CloseTape → RequestNext; интерактивный промпт CLI или
pause/resume демона `awaiting_tape`). `TapeFullError` посреди части — откат
к старому EOD (`Rewind + MTFSF(2K+1) + WriteEOF×2`) и перенос части на
следующую кассету. Restore/ReadTest следуют цепочке через тот же порт
(Reason=`restore`), сверяя ярлык и обратную ссылку `continues`.

### 4.2. `RestoreUseCase`

Три варианта, описаны в [SPECIFICATION](SPECIFICATION.md#restoreusecase):

| Вариант     | Когда используется                      | Источник позиции на ленте |
| ----------- | --------------------------------------- | ------------------------- |
| `Full`      | DR: восстановить всю ленту целиком      | перемотка в начало        |
| `Selective` | восстановить выбранные файлы из сессии  | `MTFSF(2K-1)` для сессии K |
| `Smart`     | восстановить по пути (любая сессия)     | `catalog.GetAllFileCopies(path)` |

Все три — методы одного `RestoreUseCase`, разделяют проверку xxhash.

### 4.3. `FormatTapeUseCase`

```
1. Tape.Rewind
2. (если не force) прочитать существующий label, ошибиться если есть
3. label = {Magic, FormatVersion, Name, UUID=Rand.UUID4(), FormattedAt=Clock.Now()}
4. Tape.WriteBlock(encoded(label), padToBlock)
5. Tape.WriteEOF
6. Tape.WriteEOF   ← второй EOF = пустая лента с EOD сразу после ярлыка
7. catalog.RegisterTape(uuid, name)
```

Двойной EOF после ярлыка — стандартный способ обозначить EOD на ленте
(см. [FORMAT](FORMAT.md#3-физическая-раскладка)).

## 5. Жизненный цикл

Режимы работы бинарки `lentovodec`:

| Режим             | Описание                                                        |
| ----------------- | --------------------------------------------------------------- |
| `lentovodec backup/restore/tape format` | Однократная команда; прямой доступ к ленте, без демона |
| `lentovodec daemon` | Долгоживущий процесс, держит REST API и реестр фоновых задач   |
| `lentovodec catalog/jobs/tape info` | Клиент демона: читают/меняют состояние через HTTP       |
| Web UI            | Браузер — клиент демона                                         |

Это означает: **бекап/восстановление можно делать без демона**, а каталог и
Web UI требуют запущенного `lentovodec daemon`. Схема — компромисс между
«всегда через демон» (невозможна работа без фонового сервиса) и «всегда
локально» (невозможен Web UI).

Реестр задач демона — in-memory `map[TaskID]*Task`, без персистентности:
если демон упал, активная задача теряется (но её данные на ленте уже
сохранены или нет — в зависимости от фазы).

Демон и CLI работают rootless ([SPECIFICATION §9.1](SPECIFICATION.md#91-rootless-модель)):
доступ к `/dev/nst*` — через группу `tape`, критерий доступа — успешный
probe устройства на старте, а не uid. Демон и local-команды должны
работать от одного пользователя (WAL-файлы SQLite рядом с БД).

## 6. Соглашения

### 6.1. Именование

- Один тип — один файл: `job.go`, `filemeta.go`, `session.go`.
- Конструкторы: `func NewX(deps Deps) (*X, error)` или
  `func NewX(field1 T1, field2 T2) *X`.
- Ошибки — типизированные в `domain/errors.go`: `ErrTapeFull`,
  `ErrLabelMismatch`, `ErrSessionNotFound`, и т.д. Сравнение — через
  `errors.Is`/`errors.As`, обёртка — `fmt.Errorf("...: %w", err)`.

### 6.2. Зависимости

- Все зависимости передаются через конструктор, **никаких package-level
  `var`**, кроме `var Version = "dev"` в `cmd/lentovodec/main.go`.
- `*slog.Logger` — обязательный аргумент конструктора для любого use case и
  адаптера, который может логировать.
- `context.Context` — первый аргумент всех use case-методов и всех
  long-running adapter-операций (Tape, Catalog).

### 6.3. Ошибки и паники

- `panic` разрешён только в `main.go` (на старте, если конфиг невалиден) и в
  `init`-функциях тестов. В бизнес-коде — никогда.
- Все ошибки возвращаются (`return ... , err`), не логируются и не
  проглатываются. Логирует тот, кто **получает** ошибку, а не тот, кто её
  **создаёт**.

### 6.4. Логирование

`slog` со структурными полями, без форматирования строк:

```go
logger.Info("backup started",
    slog.String("job", job.Name),
    slog.String("tape_uuid", label.UUID),
    slog.Bool("full", isFull),
)
```

Уровни: `Debug` (детали сканирования), `Info` (старт/финиш операций),
`Warn` (нефатальные проблемы: например, нет прав на chown), `Error`
(фатальные ошибки операции).

### 6.5. Конфигурация

Порядок применения (viper делает это из коробки):

```
defaults (в коде)  <  lentovodec.toml  <  env LENTOVODEC_*  <  флаги CLI
```

Файл конфигурации всегда TOML, даже если флаг `--config` указывает на
`.yaml`/`.json`.

## 7. Что явно запрещено

- Глобальные переменные кроме `cmd/lentovodec.Version`.
- `os.*`, `syscall.*`, `golang.org/x/sys/*` в `internal/domain/**` и
  `internal/usecase/**` (проверка `depguard`).
- Прямой импорт `internal/adapter/**` из `internal/usecase/**` или
  `internal/domain/**` (проверка `depguard`).
- `time.Now()` / `rand.Read()` в `domain`/`usecase` — только через `Clock`
  и `Rand` интерфейсы.
- `fmt.Println` в любом пакете, кроме `iface/cli`.
- `panic` в любом пакете, кроме `cmd/lentovodec`.
- Молчаливое проглатывание ошибок (`_ =`).
- Дублирование типов.
