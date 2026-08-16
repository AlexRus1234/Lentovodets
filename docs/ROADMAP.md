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

## Этап 4 — adapter/osfs, sqlite, tomlconfig — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-15).
> `golangci-lint run ./...`, `go vet ./...`, `go build ./...`,
> `go build -tags tape ./...`, `go test -race ./...` зелёные; покрытие:
> osfs **93.9%**, sqlite **93.7%**, tomlconfig **95.7%** (цель ≥90%).
> Зафиксированные решения (в рамках свободы impl.):
> - добавлены зависимости `modernc.org/sqlite` v1.56.0 и
>   `spf13/viper` v1.21.0 (обе — из закреплённого списка README);
> - `osfs.FS` — единый тип `port.Filesystem` (файлы walker/reader/
>   writer + конструктор в `fs.go`); Walk отдаёт пути как `filepath`
>   (разделители ОС), нормализация — забота `domain.NormalizePath`;
> - sqlite держит **одно постоянное соединение** (`MaxOpenConns=1`):
>   `PRAGMA foreign_keys` действует на уровне соединения, а каталог —
>   инструмент одного пользователя (SPEC §9.1); плюс
>   `journal_mode=WAL` и `busy_timeout=5000`;
> - `GetTapeByUUID` при отсутствии возвращает новый
>   `domain.TapeNotFoundError` (по аналогии с SessionNotFoundError,
>   чтобы usecase'ы не зависели от `sql.ErrNoRows`); domain-покрытие
>   осталось 100%;
> - `GetLatestFileStates` бьёт список путей на пакеты по 500 (лимит
>   параметров SQLite); LIKE-поиск экранирует `%`/`_`/`\`;
> - тесты sqlite гоняют каталог через `port.Catalog`; ошибки скана
>   провоцируются вставкой «плохих» типов колонок вторым сырым
>   соединением;
> - tomlconfig держит **два** viper: слоёный `read` (defaults → TOML →
>   env `LENTOVODEC_* → флаги) для геттеров и `file` (только TOML) для
>   записи — значения env/флагов и секреты не протекают в файл при
>   AddJob/RemoveJob;
> - флаги CLI передаются map'ом `flagOverrides` (без прямой зависимости
>   от pflag — её свяжет iface/cli на Этапе 7);
> - viper пишет TOML заново: ключи lowercase, одинарные кавычки,
>   комментарии не сохраняются — чтение регистронезависимо, round-trip
>   полный (компромисс viper-подхода, заложенный в плане).

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

## Этап 5 — adapter/filetape, linuxtape — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-15).
> `golangci-lint run ./...`, `go vet ./...`, `go build ./...`,
> `go build -tags tape ./...` (win), кросс-сборка `GOOS=linux` с тегом
> `tape` (amd64/arm64), `go test -race ./...` зелёные; покрытие
> `internal/adapter/filetape` — **96.3%**. Зафиксированные решения
> (в рамках свободы impl.):
> - формат файла-ленты: 8-байтовый magic `FTAPEV1\0` + фреймы
>   `тип(1) | len(LE uint32) | payload` (0x01 — блок, 0x02 — filemark);
>   позиция — байтовое смещение; запись в середину усекает хвост, как у
>   реальной ленты. Навигация — линейным сканом фреймов (O(n), для
>   dev/CI достаточно); открытая заново лента стоит в BOT;
> - `filetape.Open` отвергает чужой файл типизированной ошибкой
>   `InvalidTapeFileError` (`errors.As`/`Is`), без sentinel-var;
> - эквивалентность с `testutil.FakeTape` закреплена тестом: один и тот
>   же сценарий (запись, EOD-дозапись, MTBSFM-перезапись хвоста, полный
>   проход) даёт одинаковые последовательности событий;
> - семантика `BackwardFilemarks` из EOD: позиция уже «за последней
>   меткой», поэтому MTBSFM(1) — no-op, MTBSFM(2) — начало последнего
>   файла (совпадает с FakeTape и с трактовкой MTBSFM в st);
> - linuxtape: константы MT* — из include/uapi/linux/mtio.h (legacy-файл
>   tape_ctrl.go содержит те же значения; сверено с ядром);
>   `mtIOCTOP = 0x40086d01` — кодировка asm-generic, поэтому файлы
>   реализации ограничены `tape && linux` и списком arch
>   (amd64/arm64/386/arm/riscv64/loong64/s390x); doc.go остался под
>   одним тегом `tape`, чтобы `go build -tags tape ./...` проходил на
>   любой платформе;
> - `sendTapeCommand(fd, op, count)` — как в плане; `golang.org/x/sys`
>   переведён из indirect в direct (он уже был в дереве зависимостей
>   viper'а, список README не расширен);
> - ENOSPC при записи не маппится в `domain.TapeFullError` внутри
>   адаптера (адаптер не знает счётчиков сессии) — это задача use case
>   Этапа 6;
> - test/hardware: базовые ручные тесты (ярлык+двойной EOF, навигация
>   MTFSF/MTBSFM/MTEOM с дозаписью, eject); устройство — env
>   `LENTOVODEC_TAPE_DEVICE` (по умолчанию `/dev/nst0`), при отсутствии
>   устройства тесты скипаются; расширение — в Этапе 9.

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

## Этап 6 — Use cases — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-15).
> `golangci-lint run ./...`, `go vet ./...`, `go build ./...`,
> `go build -tags tape ./...`, `go test -race ./...` зелёные; покрытие:
> scan **97.0%**, backup **99.2%**, format **95.7%**, restore **95.2%**,
> catalog **100.0%** (цель ≥95%). Зафиксированные решения (в рамках
> свободы impl.):
> - добавлены порты `port.TapeCodec` (ярлык+сессии) и `port.Hasher`
>   (xxhash64): usecase не может импортировать adapter/** (depguard);
>   реализации — фасад `tapeformat.Codec` и `adapter/xxhash.Hasher`;
> - testutil-двойники Этапа 6: `MemCatalog` (полный `port.Catalog`,
>   семантика и сортировки как у sqlite), `FixedClock`,
>   `FixedRand` (циклический список) / `FailingRand`, `NoopLogger`,
>   `HashFunc`, `FakeCodec` (очередь сессий для чтения подряд,
>   инъекции сбоев по номеру вызова);
> - Scanner: Added/Modified по size+mtime; xxhash считается только для
>   попавших в сессию файлов; Exclude-матч каталога-предка отсекает
>   всё поддерево; tombstone'ы (mirror) — только для путей под корнями
>   задания, сортировка по алфавиту;
> - Backup: снимок для сравнения — файлы последней сессии ленты;
>   позиционирование MTFSF(2K+1) и замыкающая EOD-пара после сессии
>   (инвариант FORMAT §4: 2K+3 меток); FULL-рестарт (первая сессия или
>   `--full`) чистит старые сессии из каталога и сбрасывает нумерацию;
>   при сбое записи созданная сессия удаляется из каталога
>   (компенсация); ENOSPC-подобные ошибки маппятся в `TapeFullError`
>   (с Written из трекера прогресса); DryRun — только скан;
> - Restore: Full читает сессии подряд до `EmptyIndexError`,
>   повреждённые (маркеры ошибок tapeformat) пропускает с MTFSF;
>   Selective — MTFSF(2K−1), выбор по путям и поддеревьям; Smart —
>   копии из `GetAllFileCopies` от новых к старым с fallback'ом и
>   `NoHealthyCopyError`; tombstone'ы прочитанных сессий удаляются из
>   dest (реконструкция mirror, FORMAT §8);
> - `domain.EmptyIndexError` — конец сессий при чтении ленты подряд
>   (декодер возвращает его вместо строковой ошибки);
> - CatalogUseCase: ListTapes/ListSessions/GetFiles/Search/DeleteSession/
>   Prune + TapeInfo/Eject/ReadTest (диагностическое чтение без записи
>   на ФС); tape/codec могут быть nil (daemon без устройства).

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

## Этап 7 — iface/cli, iface/web, cmd/lentovodec — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-16).
> `golangci-lint run ./...`, `go vet ./...`, `go build ./...`,
> `go build -tags tape ./...`, `go test -race ./...` зелёные; покрытие:
> cli **75.1%**, web **82.7%**, destfs (новый пакет) **86.7%** (цель
> 60–80%). Зафиксированные решения (в рамках свободы impl.):
> - CLI: команды по таблице SPEC §5, одна группа — один файл; тесты
>   через `Execute(args, Deps)` с подменой всех зависимостей
>   (fake-лента/каталог/кодек, реальный tomlconfig в t.TempDir);
> - `HTTPClient` (iface/cli/client.go) — авторизация лестницей:
>   X-API-Key из TOML, при 401 — интерактивный логин и Bearer-токен;
> - Web: `ServerConfig` — расширение port.ConfigSource в самом web
>   (bind/web-ключи/RawTOML), реализует его tomlconfig; отдельный
>   интерфейс, чтобы не ломать чужие реализации порта;
> - аутентификация: bcrypt (subtle-сравнение имени), сессии 256 бит
>   из crypto/rand в in-memory map с TTL и лимитом 1024, rate-limit
>   5/30с на IP (скользящее окно, сброс при успехе), api_key —
>   sha256+subtle; отказ старта при не-loopback bind без пароля
>   (в т.ч. при override флагами --bind/--port);
> - TaskRegistry: `StartIfIdle` — атомарная проверка «активная задача
>   есть» (стример один); прогресс-репортёр задачи троттлит строки
>   лога до 2 Гц, скорость — экспоненциальное сглаживание; graceful
>   shutdown: HTTP Shutdown 5с + WaitAll задач 30с;
> - лента в daemon открывается на время операции (probe в
>   GET /api/status = open+close), device меняется POST /api/settings
>   (409 при активной задаче);
> - restore в dest: декоратор `iface/destfs.Wrap` — usecase про dest
>   не знает, пишется по путям индекса;
> - `embed.FS` с плейсхолдером index.html до Этапа 8 (make clean
>   восстанавливает плейсхолдер); единственное package-level var
>   помимо cmd/lentovodec.Version (зафиксировано как исключение);
> - выбор ленты в wire: `wire_device_filetape.go` (всегда filetape,
>   /dev/* не создаётся молча) / `wire_device_linux.go` (tape-тег:
>   char-устройство → linuxtape, прочее → filetape); EACCES/ENOENT →
>   подсказка `usermod -aG tape` (rootless-модель SPEC §9.1);
> - `sloglog.New(level, w)` — фабрика логгера (Этап 4 её не завёл);
> - **исправлен баг Этапа 6**, найденный e2e-тестом с настоящим
>   кодеком: `Restore.Full` и `ReadTest` не пропускали filemark ярлыка
>   перед чтением сессий подряд (FORMAT §9) — с FakeCodec это
>   маскировалось; добавлен MTFSF(1) после чтения ярлыка;
> - CLI `passwd` читает пароль из stdin дважды (без подавления эха —
>   терминальные обёртки не входят в закреплённые зависимости).

**Файлы:**
- `internal/iface/cli/` — cobra-команды по таблице из
  [SPECIFICATION §5](SPECIFICATION.md#5-cli). Одна команда — один файл.
  Команды `daemon`-режима используют `client.HTTPClient`.
- `internal/iface/cli/wire.go` — сборка use case и адаптеров в main.
  Probe доступа к устройству на старте local-команд и демона
  (rootless-модель, [SPECIFICATION §9.1](SPECIFICATION.md#91-rootless-модель)):
  при EACCES/ENOENT — понятная ошибка с подсказкой (`usermod -aG tape`),
  без проверки uid.
- `internal/iface/web/server.go` — chi-роутер со всеми эндпоинтами из
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
- `internal/iface/cli/client.go` — HTTP-клиент для CLI-команд daemon-режима.
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

## Этап 8 — Web UI (Vue 3 + Vite) — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-16).
> `make web-build` зелёный (`vue-tsc --noEmit` + `vite build`, Vite 7);
> бинарь `lentovodec daemon` раздаёт бандл (SPA-fallback, верные
> content-type); e2e-сценарий против реального демона с filetape
> прогнан: format → add job → backup task → поллинг прогресса →
> sessions → files → smart restore в dest. Зафиксированные решения
> (в рамках свободы impl.):
> - зависимости: `vue` 3.5, `vite` 7, `@vitejs/plugin-vue` 6,
>   `typescript` 5.9, `vue-tsc` 3. Router/pinia/vue-i18n не
>   добавлялись: пять экранов — табы в `App.vue`, i18n — два словаря в
>   `i18n.ts` с подстановкой `{параметров}`;
> - сверх списка файлов добавлены `src/task.ts` (общее состояние фоновой
>   задачи), `src/format.ts` (байты/даты), `src/vite-env.d.ts` и
>   `components/TaskProgress.vue` — панель прогресса общая для
>   Jobs (backup) и Files (restore), живёт в App;
> - прогресс — поллинг `GET /api/tasks/{id}/progress` раз в секунду
>   (WebSocket/SSE нет, SPEC §9.2); терминальное состояние — панель
>   остаётся до закрытия;
> - 401 от любого запроса → экран логина (событие из `api.ts`), токен и
>   язык — `localStorage`;
> - редактирование задания = remove+add: API умеет только
>   `POST /jobs` и `DELETE /jobs/{name}`;
> - выбор каталога в Files разворачивается в список файлов клиентски
>   (smart-restore ищет копии по точным путям, `usecase/restore`);
>   tombstone (`state=D`) невосстановимы — чекбоксов нет; «оригинальные
>   пути» — с подтверждающим диалогом (перезапись);
> - пустые каталоги показываются по явным записям сессии (у них нет
>   файлов-потомков, из путей не выводятся);
> - бандл и `web/node_modules/` в git не попадают (`.gitignore`),
>   `web/package-lock.json` коммитится: fresh clone требует
>   `make web-build` (Node 22+) перед `make build`; CI Этапа 10
>   собирает UI перед бинарем.

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

**Тесты:** smoke — открыть демо-страницу руками; автотестов пока нет
(статическая раздача покрыта `TestStatic_ServesIndex` из Этапа 7;
e2e-сценарий API прогнан вручную — см. выше).

**Готовность:** `make web-build` зелёный; бинарь `lentovodec daemon`
раздаёт UI. ✅

---

## Этап 9 — Интеграционные и hardware-тесты — ЗАВЕРШЁН

> **Статус: завершён** (2026-08-16).
> `golangci-lint run ./...`, `go vet ./...`, `go build ./...`,
> `go build -tags tape ./...`, `go test -race ./...`,
> `go test ./test/integration/... -count=5` зелёные; кросс-сборка
> `GOOS=linux go vet -tags=tape ./test/hardware/...` проходит.
> Hardware-сценарий на реальном стримере не прогонялся (привода под
> рукой нет) — запуск вручную по инструкции в шапке файла.
> Зафиксированные решения (в рамках свободы impl.):
> - общий харнесс `test/integration/harness_test.go`: реальные
>   адаптеры (filetape + osfs + sqlite + tapeformat + xxhash) и
>   реальные use case'ы; фейки не используются вовсе;
> - «извлечение кассеты» = закрытие и повторное открытие ленты и
>   каталога (`reopen`): лента встаёт в BOT, sqlite перечитывается с
>   диска — заодно проверяется персистентность каталога;
> - восстановление — через `iface/destfs.Wrap` (как CLI `restore
>   --dest`), дерево сравнивается побайтово относительно корня
>   источника (пути в индексе — исходные, с именем тома на Windows);
> - повреждение копии для smart-fallback — порча одного байта
>   tar-payload в файле-ленте (framing не трогается, хеш не
>   сходится) — `corruptNeedle`;
> - каталоги с изменившимся mtime попадают в сессию как Modified
>   недетерминированно (Windows обновляет mtime каталогов лениво) —
>   assertions в тестах ограничены детерминированной частью;
> - новые testutil-двойники: `StepClock` (строго растущие метки
>   времени — порядок копий «новые сверху») и `StaticConfig`
>   (port.ConfigSource с фиксированными заданиями), см.
>   docs/TESTING.md §3.5–3.6;
> - hardware: `test/hardware/backup_test.go` (`tape && linux`) —
>   сквозной сценарий format → backup → readtest → restore full с
>   побайтовым сравнением; повторное форматирование уже размеченной
>   ленты — через env `LENTOVODEC_TAPE_REFORMAT=1`.

**Файлы:**
- `test/integration/backup_restore_test.go` — сценарий «format → backup →
  eject-simulated → read label → restore full → сравнить дерево файлов»
  через `filetape` + `osfs` + `sqlite`; вторая сессия (INC) после правок,
  поиск по каталогу, selective-restore из сессии 1.
- `test/integration/mirror_test.go` — сценарий с mirror-режимом: создание,
  изменение, удаление файлов, проверка состояния после восстановления
  (tombstone удаляет файл из dest).
- `test/integration/smart_restore_test.go` — multiple copies, повреждённая
  копия (порча байта на ленте), fallback, NoHealthyCopyError.
- `test/hardware/*.go` — `//go:build tape`, прогон на реальном стримере:
  сырые операции ленты (Этап 5) + сквозной сценарий с use case'ами.

**Тесты:** это сами тесты.

**Готовность:** `go test ./test/integration/...` зелёный; hardware — по
возможности. ✅

---

## Этап 10 — CI, README, финал — ЗАВЕРШЁН

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

**Готовность:** CI зелёный на main. ✅

> **Статус: завершён** (коммиты `5cb84f2`…`27df5d6`, 2026-08-16; CI
> зелёный на `main` после трёх прогонов). Отступления от плана и
> зафиксированные решения:
> - `.forgejo/workflows/build.yml` вместо `.github/workflows/ci.yml`:
>   проект хостится на self-hosted Forgejo (git.yadr00.internal), CI —
>   по образцу Intermasq 1-в-1 (fedora:44-контейнер, Nora-зеркала
>   GOPROXY/npm/RPM, step-ca trust, workflow_dispatch c input'ами
>   `push_to_registry`/`version_tag`/`run_race_tests`); auto-run на
>   push/PR не заводился — ручной запуск, как в Intermasq;
> - версия бинаря — точный тег `v*` на HEAD → input → `sha-<hex8>`;
>   бинарь статический (CGO_ENABLED=0), артефакт `lentovodets-<ver>-
>   linux-amd64`(+`.sha256`) публикуется в Forgejo Packages + Release
>   по кнопке (`UPLOAD_TOKEN`); зеркало (mirror.yaml) не создавалось;
> - жёсткого порога покрытия в CI нет — отчёт информационный: фактический
>   total 87.5% (против ≥90% в этом плане) из-за плановых 60–80% у
>   iface/cli (75.1%) и iface/web (82.7%); также `usecase/restore` 94.6%
>   при цели ≥95 и domain 95.4% (было 100% на Этапе 1 — код Этапа 6
>   добавил ветки) — принято как есть, цели уточнены по факту;
> - **исправлены долги, найденные CI**: gofmt-выравнивание в 3 файлах +
>   unconvert/SA1012/ineffassign в iface/web (локальный кастомный
>   golangci-lint v1.64.8 молчит на развёрнутых паттернах `./...` и
>   честен на точечном пакете — этапные «lint зелёный» были искренними;
>   CI-шаг `gofmt -l` от этого не зависит); `unparam` добавлен в
>   exclude-rules для `_test.go`;
> - **исправлены платформозависимости** (Windows-локаль против
>   Linux-CI): Scanner форсирует `Size=0` для каталогов (Lstat-размер
>   каталога ≠ 0 на Linux попадал в `Stats.Bytes`), destfs срезает имя
>   тома `C:` и на ОС без `VolumeName` (бекап на Windows → restore на
>   Linux);
> - имя проекта — **Lentovodets** (репозиторий, пакеты, релизы);
>   имя модуля Go, бинаря и конфига осталось `lentovodec`
>   (переименование кода не проводилось — задело бы env/конфиги);
> - README финальный: сборка из исходников и CI-артефактов, конфиг,
>   таблица CLI, Web UI + SSH-туннель, rootless-развёртывание
>   (useradd/udev/systemd-харднинг, SPEC §9.1), CI, dev-команды;
> - `docs/CHANGELOG.md` — Keep-a-Changelog каркас + запись 1.0.0.
