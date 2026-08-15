# Roadmap

Пошаговый план реализации. Каждый этап — атомарная единица: после него код
компилируется, тесты проходят, можно делать коммит.

**Порядок выполнения — строгий:** каждый этап опирается на предыдущий.

Для каждого этапа указано:
- **Файлы** — что создаётся.
- **Тесты** — что проверяется.
- **Готовность** — критерий перехода к следующему.

---

## Этап 0 — Подготовка инфраструктуры — ЗАВЕРШЁН

> **Статус: завершён** (коммит `cca5729`, 2026-08-15).
> `make lint`, `go build ./...`, `go build -tags tape ./...`, `go test ./...`
> зелёные. Отступление от плана: `legacy/nil-backup/` (~1.5 ГБ) оставлен
> только как локальный референс и в git не коммитится (см. `.gitignore`).

**Файлы:**
- Перенос `nil-backup/` → `legacy/nil-backup/`.
- Удалить корневой старый `go.mod` (3 строки, бесполезный).
- `go.mod`, `go.sum` для модуля `lentovodec`.
- `.gitignore`, `.editorconfig`.
- `.golangci.yaml` (конфиг ниже).
- `Makefile` с целями: `lint`, `test`, `test-race`, `cover`, `build`,
  `web-build`, `web-dev`, `clean`.
- Скелет `internal/domain/doc.go`, `internal/port/doc.go`, и т.д. —
  пустые пакеты с одним `// Package xxx ...` комментарием, чтобы импорты
  не падали.
- `git init`, первый коммит.

**`golangci.yaml` (ключевые правила):**

```yaml
run:
  timeout: 5m
  tests: true

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - ineffassign
    - gocritic
    - revive
    - gocyclo
    - depguard
    - gofmt
    - goimports

linters-settings:
  gocyclo:
    min-complexity: 15
  depguard:
    rules:
      no-os-in-domain:
        list-mode: lax
        files:
          - "**/internal/domain/**"
          - "**/internal/usecase/**"
        deny:
          - pkg: "os"
          - pkg: "syscall"
          - pkg: "golang.org/x/sys"
          - pkg: "net"
      no-adapter-in-core:
        list-mode: lax
        files:
          - "**/internal/domain/**"
          - "**/internal/usecase/**"
          - "**/internal/port/**"
        deny:
          - pkg: "**/internal/adapter/**"
```

**Тесты:** `go vet ./...` проходит.

**Готовность:** `make lint` зелёный на пустых пакетах, `go build ./...`
собирается.

---

## Этап 1 — Domain layer — ЗАВЕРШЁН

> **Статус: завершён** (коммит `8120357`, 2026-08-15).
> `make lint`, `go build ./...`, `go build -tags tape ./...`,
> `go test -race ./...` зелёные; покрытие `internal/domain` — **100.0%**.
> Зафиксированные решения (в рамках свободы impl.):
> - `FileMeta.ModTime` — Unix-**наносекунды** (SPEC §2.4 просил зафиксировать);
> - ошибки — структуры с полями-деталями и методами `Is`/`Error`,
>   sentinel-переменных нет (запрет package-level var, ARCHITECTURE §6.2);
>   сравнение: `errors.As(err, &domain.TapeFullError{})` или
>   `errors.Is(err, &domain.TapeFullError{})`;
> - семантика `MatchExclude`: шаблон без `/` матчит **базовое имя**
>   (примеры SPEC `*.tmp`, `node_modules` работают «вглубь»); используется
>   `path.Match`, а не `filepath.Match` — у последнего на windows `*`
>   пересекает `/`, что делало бы фильтры платформозависимыми;
> - добавлена зависимость `bmatcuk/doublestar/v4` v4.10.0 (из
>   закреплённого списка README).

**Файлы:**
- `internal/domain/job.go` — `Job`, `JobMode`, валидация (`Mode` ∈
  `{append, mirror}`).
- `internal/domain/filemeta.go` — `FileMeta`, `FileState` (`Added`,
  `Modified`, `Deleted`), методы `IsAdded()`, `IsDeleted()`.
- `internal/domain/session.go` — `Session`, `SessionType` (`FULL`, `INC`).
- `internal/domain/tape.go` — `TapeLabel`, `TapeInfo`, константы `Magic`,
  `FormatVersion`, `BlockSize`.
- `internal/domain/filter.go` — `MatchExclude(path, patterns) bool`,
  `NormalizePath(p) string`. Использует `path/filepath.Match` для
  одиночных шаблонов и `doublestar` для `**`.
- `internal/domain/errors.go` — `ErrTapeFull`, `ErrLabelMismatch`,
  `ErrForeignFormat`, `ErrBlankTape`, `ErrNewerFormat`, `ErrSessionNotFound`,
  `ErrNoHealthyCopy`, `ErrAlreadyFormatted`. Все реализуют `Is`/`As`.

**Тесты:** table-driven на каждый файл, 100% coverage.

**Готовность:** `go test -cover ./internal/domain` ≥ 100% (или 95%+ с
учётом недостижимых веток error-path), `make lint` зелёный.

---

## Этап 2 — Port layer — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-15).
> `make lint`, `go vet ./...`, `go build ./...`, `go build -tags tape ./...`
> зелёные. Зафиксированные решения (в рамках свободы impl.):
> - `Tape.EndOfData` (MTEOM) — имя из плана; синоним `LocateEOD` из
>   ARCHITECTURE §4.1 не заводился;
> - `ReadBlock` возвращает `io.EOF` при достижении filemark'а/EOD —
>   контракт, на который опирается декодер Этапа 3;
> - добавлена композиция `port.Filesystem` (Walker+FileReader+FileWriter)
>   для передачи одной зависимостью в use case;
> - вспомогательные типы каталога: `port.TapeRecord` (проекция таблицы
>   `tapes`; `domain.TapeLabel` не подошёл из-за `FormattedAt string`
>   RFC-3339 против `INTEGER` в БД) и `port.FileCopy` (общий результат
>   `GetAllFileCopies` и `SearchFiles`);
> - `ListSessions(ctx, tapeUUID)` — фильтр строкой, `""` = все кассеты;
> - `SearchFiles` — поиск по подстроке в пути;
> - добавлен `port.ConfigEditor` (AddJob/RemoveJob): нужен iface/web и
>   CLI `jobs add/remove` (Этапы 4, 7), а iface не может импортировать
>   адаптеры напрямую.

**Файлы** (каждый — набор интерфейсов, без реализаций):
- `internal/port/tape.go`:

  ```go
  type Tape interface {
      ReadBlock(ctx) ([]byte, error)        // чтение одного блока до BlockSize
      WriteBlock(ctx, []byte) error          // запись блока (добивает до BlockSize нулями)
      WriteEOF(ctx) error                    // MTWEOF
      ForwardFilemarks(ctx, n int) error     // MTFSF
      BackwardFilemarks(ctx, n int) error    // MTBSFM
      Rewind(ctx) error                      // MTREW
      EndOfData(ctx) error                   // MTEOM
      Eject(ctx) error                       // MTOFFL
      Close() error
  }
  ```

- `internal/port/filesystem.go`:

  ```go
  type Walker interface {
      Walk(ctx, root string, fn func(path string, info Entry) error) error
  }
  type FileReader interface {
      Open(path string) (io.ReadCloser, error)
      Stat(path string) (Entry, error)
  }
  type FileWriter interface {
      MkdirAll(path string, perm os.FileMode) error
      Create(path string) (io.WriteCloser, error)
      Remove(path string) error
  }
  type Entry interface {
      Name() string
      Size() int64
      ModTime() time.Time
      IsDir() bool
      Mode() os.FileMode
  }
  ```

  Замечание: `os.FileMode` и `time.Time` можно использовать в портах — это
  типы, а не I/O. `depguard` разрешает.

- `internal/port/catalog.go` — все методы из [SPECIFICATION §3.2](SPECIFICATION.md#32-контракт-portcatalog).
- `internal/port/progress.go` — `ProgressReporter` с throttle 2 Гц.
- `internal/port/clock.go` — `Clock` с методом `Now() time.Time`.
- `internal/port/rand.go` — `Rand` с методом `UUID4() (string, error)`.
- `internal/port/config.go` — `ConfigSource` с `Jobs() ([]domain.Job, error)`
  и `Device()/DB()/...`.

**Тесты:** не тестируются (только интерфейсы).

**Готовность:** компилируется, `make lint` зелёный.

---

## Этап 3 — adapter/tapeformat (чистый формат) — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-15).
> `make lint`, `go vet ./...`, `go build ./...`, `go build -tags tape ./...`,
> `go test -race ./...` зелёные; покрытие `internal/adapter/tapeformat` —
> **100.0%**. Зафиксированные решения (в рамках свободы impl.):
> - сигнатуры расширены против кратких в плане: `WriteSession(ctx, tape,
>   idx SessionIndex, fs, prog)` / `ReadSession(ctx, tape, dest, prog)` —
>   индексу нужны метаданные сессии (session_num/type/job_run_id/
>   timestamp/job_name, FORMAT §6), а `context` — первый аргумент
>   long-running операций (ARCHITECTURE §6.2);
> - wire-тип `SessionIndex` живёт в tapeformat;
> - `ReadSession` с `dest == nil` — режим проверки (readtest): содержимое
>   читается, хеши сверяются, на ФС не пишется;
> - после `ReadSession` лента стоит за filemark'ом tar-сегмента — сессии
>   можно читать подряд (остаток сегмента дочитывается до метки);
> - tar — GNU (FORMAT §7); ModTime в tar-заголовке — секунды
>   (наносекунды — только в индексе; GNU не кодирует sub-second);
> - энкодер сверяет xxhash при записи: файл, изменившийся между сканом и
>   записью, — ошибка, а не тихая порча индекса;
> - прогресс — только `Update` (фаза `write`); `Done`/`Fail` публикует
>   вызывающий use case; `prog == nil` допустим;
> - `EncodeLabel` возвращает `([]byte, error)` (контроль «JSON влезает в
>   один блок»); ошибки декодирования ярлыка — типизированные:
>   `BlankTapeError`/`ForeignFormatError`/`NewerFormatError`;
> - созданы необходимые testutil-двойники (docs/TESTING.md §3):
>   `FakeTape`, `MapFS` (Reader+Writer+Walker), `NoProgress`; остальные
>   (`MemCatalog`, `FixedClock`, …) — в Этапе 6;
> - golden-файлы `golden/{label,index,session}.bin`,
>   `session_marks.txt`; регенерация при осознанном изменении формата:
>   `go test ./internal/adapter/tapeformat -update`;
> - добавлена зависимость `github.com/cespare/xxhash/v2` v2.1.2 (из
>   закреплённого списка README).

**Файлы:**
- `internal/adapter/tapeformat/encoder.go` —
  `WriteSession(tape port.Tape, files []domain.FileMeta, fs port.FileReader,
   prog port.ProgressReporter) error`. Под капотом: JSON-кодирует индекс,
  добивает до `BlockSize`, пишет в ленту по блокам, `WriteEOF`; затем пишет
  tar-поток, `WriteEOF`.
- `internal/adapter/tapeformat/decoder.go` —
  `ReadSession(tape port.Tape, dest port.FileWriter, prog port.ProgressReporter)
   ([]domain.FileMeta, error)`. Читает индекс, потом tar; проверяет xxhash.
- `internal/adapter/tapeformat/label.go` — `EncodeLabel(label) []byte`,
  `DecodeLabel(block []byte) (domain.TapeLabel, error)`.
- `internal/adapter/tapeformat/golden/` — директория с golden-файлами для
  тестов (байты записанной сессии).

**Тесты:** используется `FakeTape` из `internal/testutil`; проверка
round-trip (записали — прочитали — сравнили); golden-файлы для байтового
сравнения индекса и ярлыка.

**Готовность:** `go test -cover ./internal/adapter/tapeformat` ≥ 100%.
Это самый важный тестовый модуль: если формат детерминирован и обратим,
всё остальное строится на нём.

---

## Этап 4 — adapter/osfs, sqlite, tomlconfig

**Файлы:**
- `internal/adapter/osfs/walker.go`, `reader.go`, `writer.go` — реализации
  `port.Walker/Reader/Writer` поверх `os.*`.
- `internal/adapter/sqlite/catalog.go` — реализация `port.Catalog`.
  Схема из [SPECIFICATION §3.1](SPECIFICATION.md#31-схема). В конструкторе:
  `PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`.
- `internal/adapter/sqlite/migrations.go` — единственный `CREATE TABLE IF
  NOT EXISTS`, без миграционного движка (пока).
- `internal/adapter/tomlconfig/config.go` — реализация `port.ConfigSource`
  на viper'е. Слои применения: defaults → TOML → env → флаги.
- `internal/adapter/tomlconfig/jobs.go` — `AddJob`/`RemoveJob` с записью
  обратно в TOML.

**Тесты:**
- osfs: `t.TempDir()`, walk/stat/open/create/remove — 90%+.
- sqlite: `"file::memory:?cache=shared"` или tempfile, все методы — 90%+.
- tomlconfig: `t.TempDir()`, чтение/запись TOML — 90%+.

**Готовность:** `go test -cover ./internal/adapter/{osfs,sqlite,tomlconfig}`
≥ 90%.

---

## Этап 5 — adapter/filetape, linuxtape

**Файлы:**
- `internal/adapter/filetape/filetape.go` — реализация `port.Tape` поверх
  обычного файла. Хранит байты + slice позиций filemark'ов. Для dev/CI.
  Один файл = одна лента; для тестов создаётся в `t.TempDir()`.
- `internal/adapter/linuxtape/linuxtape.go` — реализация `port.Tape` через
  `os.OpenFile("/dev/nst0", ...)` + `golang.org/x/sys/unix.Ioctl`. Под
  build tag `//go:build tape`.
- `internal/adapter/linuxtape/ops.go` — константы `MTIOCTOP`, `MTWEOF`,
  `MTFSF`, `MTBSFM`, `MTREW`, `MTEOM`, `MTOFFL`. Универсальный
  `sendTapeCommand(fd, op, count)`.

**Тесты:**
- filetape: full round-trip в `*_test.go` (без build tag).
- linuxtape: в `test/hardware/tape_test.go` с `//go:build tape`, запускается
  вручную.

**Готовность:** `go test -cover ./internal/adapter/filetape` ≥ 95%;
`go build -tags tape ./...` компилируется.

---

## Этап 6 — Use cases

**Файлы:**
- `internal/usecase/scan/scanner.go` — `Scanner.Scan(ctx, job, lastSnapshot)
  ([]domain.FileMeta, error)`. Реализует оба режима (`append`/`mirror`):
  - Оба: `Added`, `Modified` через сравнение size+mtime+xxhash.
  - `mirror`: для путей из прошлого снимка, отсутствующих в текущем —
    `Deleted`.
- `internal/usecase/backup/usecase.go` — `BackupUseCase` с методом
  `Backup(ctx, jobName, opts) (Result, error)`. Алгоритм из
  [ARCHITECTURE §4.1](ARCHITECTURE.md#41-backupusecasebackupctx-jobname-opts).
- `internal/usecase/restore/usecase.go` — `RestoreUseCase` с методами
  `Full`, `Selective`, `Smart`.
- `internal/usecase/format/usecase.go` — `FormatTapeUseCase`.
- `internal/usecase/catalog/usecase.go` — `CatalogUseCase`.

**Тесты:** все через фейки из `internal/testutil`:
- `FakeTape` — bytes.Buffer + slice of filemark positions.
- `MapFS` — обёртка над `fstest.MapFS` под `port.Filesystem`.
- `MemCatalog` — in-memory реализация `port.Catalog`.
- `NoProgress` — заглушка `ProgressReporter`.
- `FixedClock`, `FixedRand`.

Цель — 95%+ coverage по всем use case. Проверяются:
- happy path;
- отмена контекста;
- ошибки адаптеров (фейк подменяется на возвращающий ошибку);
- конкретные типизированные ошибки (через `errors.Is`).

**Готовность:** `go test -cover ./internal/usecase/...` ≥ 95%.

---

## Этап 7 — iface/cli, iface/web, cmd/lentovodec

**Файлы:**
- `internal/iface/cli/` — cobra-команды по таблице из
  [SPECIFICATION §5](SPECIFICATION.md#5-cli). Одна команда — один файл.
  Команды `daemon`-режима используют `client.HTTPClient`.
- `internal/iface/cli/wire.go` — сборка use case и адаптеров в main.
- Probe доступа к устройству на старте local-команд и демона
  (rootless-модель, [SPECIFICATION §9.1](SPECIFICATION.md#91-rootless-модель)):
  при EACCES/ENOENT — понятная ошибка с подсказкой (`usermod -aG tape`),
  без проверки uid.
- `internal/iface/web/router.go` — chi-роутер со всеми эндпоинтами из
  [SPECIFICATION §6](SPECIFICATION.md#6-rest-api).
- `internal/iface/web/auth.go` — аутентификация по SPEC §6.0/§9.2:
  bcrypt-проверка логина, in-memory сессии (crypto/rand, TTL), middleware
  `Authorization: Bearer` / `X-API-Key` (subtle-сравнение), rate-limit
  на `/auth/login` (5/30с на IP), аудит-лог; отказ старта при не-loopback
  bind без `web_password_hash`.
- `internal/iface/web/handler_*.go` — обработчики по группам (auth, tape,
  jobs, catalog, tasks).
- `internal/iface/web/taskregistry.go` — in-memory `map[TaskID]*Task` с
  мьютексом; фоновые goroutine для backup/restore.
- `internal/iface/web/embed.go` — `//go:embed assets/*` (бандл из Этапа 8).
- `internal/iface/web/client.go` — HTTP-клиент для CLI-команд daemon-режима
  (может лежать в `internal/iface/cli/client.go`).
- `cmd/lentovodec/main.go` — `package main`, ~50 строк, только вызов
  `cli.Execute()` после парсинга флагов.

**Тесты:**
- cli: `execute(args) -> (stdout, err)` на нескольких сценариях.
- web: `httptest.NewServer` + запросы к каждому эндпоинту; use cases под
  фейками.
- auth: логин (верный/неверный пароль, rate-limit → 429, сброс счётчика),
  401 без заголовка, `X-API-Key`, TTL-сессии, отказ старта при LAN-bind
  без пароля.
- taskregistry: гонка (`-race`) на фоне нескольких задач.

**Готовность:** `go test -cover ./internal/iface/...` ≥ 70%; `go build
./cmd/lentovodec` собирается.

---

## Этап 8 — Web UI (Vue 3 + Vite)

**Файлы:**
- `web/package.json`, `web/vite.config.ts` (build →
  `../internal/iface/web/assets`), `web/tsconfig.json`.
- `web/src/main.ts`, `web/src/App.vue`.
- `web/src/components/Login.vue` (экран логина, токен в `localStorage`),
  `Tape.vue`, `Jobs.vue`, `Catalog.vue`, `Files.vue`.
- `web/src/i18n.ts` (ru/en).
- `web/src/api.ts` — обёртка над fetch.
- `web/index.html`.

Сборка:

```bash
cd web && npm run build
# выход в ../internal/iface/web/assets/
```

В прод-режиме embed подхватывает это; в dev-режиме — `npm run dev` с
прокси на `:29201`.

**Тесты:** smoke — открыть демо-страницу руками; автотестов пока нет.

**Готовность:** `make web-build` зелёный; бинарь `lentovodec daemon`
раздаёт UI.

---

## Этап 9 — Интеграционные и hardware-тесты

**Файлы:**
- `test/integration/backup_restore_test.go` — сценарий «format → backup →
  eject-simulated → read label → restore full → сравнить дерево файлов»
  через `filetape` + `osfs` + `sqlite`.
- `test/integration/mirror_test.go` — сценарий с mirror-режимом: создание,
  изменение, удаление файлов, проверка состояния после восстановления.
- `test/integration/smart_restore_test.go` — multiple copies, повреждённая
  копия, fallback.
- `test/hardware/*.go` — `//go:build tape`, прогон на реальном стримере.

**Тесты:** это сами тесты.

**Готовность:** `go test ./test/integration/...` зелёный; hardware — по
возможности.

---

## Этап 10 — CI, README, финал

**Файлы:**
- `.github/workflows/ci.yml` — на push/PR:
  - checkout, setup Go 1.26, setup Node.
  - `make lint`.
  - `make test-race`.
  - `make cover` с порогом (например, `>= 90%` общего, `>= 95%` по
    `internal/domain`, `internal/usecase`, `internal/adapter/tapeformat`).
  - `make web-build`.
  - `make build`.
  - загрузка бинарника как артефакта.
- `README.md` финальный (installing/usage/contributing + rootless-развёртывание:
  пользователь `lentovodec`, группа `tape`, udev, systemd-юнит с харднингом —
  SPECIFICATION §9.1).
- Возможно `docs/CHANGELOG.md` — пустой каркас.

**Готовность:** CI зелёный на main.
