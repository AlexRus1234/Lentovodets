# Стратегия тестирования

Этот документ описывает, **как** мы достигаем 95%+ coverage по
бизнес-логике без необходимости в реальном стримере.

## 1. Принципы

| Принцип                  | Что значит                                                  |
| ------------------------ | ----------------------------------------------------------- |
| Тест через интерфейс     | Каждый use case / адаптер тестируется через `port.*`        |
| Один тест — один файл    | `foo.go` ↔ `foo_test.go`                                    |
| Table-driven по умолчанию| Сценарии — слайс структур, прогон в цикле                   |
| Имена: `TestXxx_ScenarioYyy` | Легко искать, понятный вывод                            |
| `-race` обязательно      | Любой тест с goroutine — под флагом `-race`                 |
| Нет testing.T.Logf в прод | Используем только для отладки                              |

## 2. Уровни

| Уровень       | Где живёт                          | Build tag | Тестирует                |
| ------------- | ---------------------------------- | --------- | ------------------------ |
| Unit          | `*_test.go` рядом с кодом          | —         | одну функцию/метод       |
| Integration   | `test/integration/*_test.go`       | —         | несколько адаптеров + use case |
| Hardware      | `test/hardware/*_test.go`          | `tape`    | реальный стример         |

**Запуск:**

```bash
go test ./...                              # unit + integration
go test -race ./...                        # то же + race detector
go test -tags=tape ./test/hardware/...     # только вручную, на машине со стримером
go test -cover ./internal/...              # coverage по бизнес-логике
```

## 3. Test doubles

Все живут в `internal/testutil/`, доступны из `*_test.go` в любых пакетах
(включая `internal/...`) через обычный импорт. Это допустимое исключение
из «правила зависимостей», потому что `testutil` сам зависит только от
`port`/`domain`.

### 3.1. `FakeTape`

```go
type FakeTape struct {
    mu        sync.Mutex
    blocks    [][]byte        // все записанные блоки по порядку
    marks     []int           // индексы блоков, после которых стоит EOF
    readPos   int             // позиция чтения (в блоках)
    markIdx   int             // текущий «уровень» filemark'а
}

func NewFakeTape() *FakeTape
func (t *FakeTape) ReadBlock(ctx) ([]byte, error)
func (t *FakeTape) WriteBlock(ctx, b []byte) error
func (t *FakeTape) WriteEOF(ctx) error
func (t *FakeTape) ForwardFilemarks(ctx, n int) error
func (t *FakeTape) BackwardFilemarks(ctx, n int) error
func (t *FakeTape) Rewind(ctx) error
func (t *FakeTape) EndOfData(ctx) error
func (t *FakeTape) Eject(ctx) error
func (t *FakeTape) Close() error

// Для assertions в тестах:
func (t *FakeTape) BlockCount() int
func (t *FakeTape) MarkCount() int
func (t *FakeTape) Snapshot() (blocks [][]byte, marks []int)
```

Особенности:
- `WriteBlock` принимает байты любого размера; хранит как есть.
- `WriteEOF` добавляет запись в `marks` (текущий `len(blocks)`).
- `ForwardFilemarks(n)` перемещает `readPos` на первый блок после n-го
  filemark'а.
- `EndOfData` прыгает в конец (после последнего filemark'а).
- Поведение детерминировано повторяет семантику `MTFSF`/`MTWEOF`, поэтому
  любые тесты format'а ленты на фейке корректны.

### 3.2. `MapFS`

Тонкая обёртка над `testing/fstest.MapFS` для соответствия `port.Filesystem`.
В тестах:

```go
fs := testutil.NewMapFS(map[string]string{
    "/etc/nginx/nginx.conf": "worker_processes auto;\n",
    "/etc/hosts":            "127.0.0.1 localhost\n",
})
```

### 3.3. `MemCatalog`

Полная in-memory реализация `port.Catalog` (slayce map'ов с мьютексом).
Используется в use case-тестах, чтобы не зависеть от SQLite.

### 3.4. `NoProgress`

```go
type NoProgress struct{}
func (NoProgress) Report(_ port.ProgressEvent) {}
```

### 3.5. `FixedClock` / `StepClock` / `FixedRand`

```go
clock := testutil.FixedClock(time.Unix(1700000000, 0))
clock := testutil.StepClock(time.Unix(1700000000, 0)) // +1с на каждый Now()
rand := testutil.FixedRand("550e8400-e29b-41d4-a716-446655440000")
```

`FixedRand.UUID4()` всегда возвращает предзагруженную строку; для
нескольких вызовов — по очереди из слайса. Это даёт детерминированные
JSON-ярлыки в golden-тестах. `StepClock` даёт строго возрастающие метки
времени (форматирование, сессии) — нужен интеграционным тестам, где
порядок «новые сверху» зависит от timestamp.

### 3.6. `StaticConfig`

Двойник `port.ConfigSource` с фиксированным списком заданий (поле
`JobList`); остальные геттеры возвращают нулевые значения. Нужен
интеграционным и hardware-тестам, собирающим реальный
`backup.UseCase` без TOML-файла.

## 4. Coverage цели

| Пакет                                | Цель     | Почему                          |
| ------------------------------------ | -------- | ------------------------------- |
| `internal/domain/**`                 | 100%     | чистые функции                  |
| `internal/usecase/**`                | ≥ 95%    | фейки I/O                       |
| `internal/adapter/tapeformat`        | 100%     | круглый байтовый round-trip     |
| `internal/adapter/osfs`              | ≥ 90%    | `t.TempDir()`                   |
| `internal/adapter/sqlite`            | ≥ 90%    | `:memory:`                      |
| `internal/adapter/tomlconfig`        | ≥ 90%    | `t.TempDir()`                   |
| `internal/adapter/filetape`          | ≥ 95%    | как FakeTape, но файл-основанный |
| `internal/adapter/linuxtape`         | 0% unit  | железо, build tag               |
| `internal/iface/cli`                 | 60-70%   | тонкая обёртка                  |
| `internal/iface/web`                 | 70-80%   | handler-тесты                   |
| **Итог по `internal/`**              | **≥ 90%**|                                 |

Эти цифры проверяются в CI (Этап 10) через `go test -coverprofile` + скрипт
или `go-toolcover` с порогами.

## 5. Что проверяем в use case-тестах

Для каждого метода:

1. **Happy path** — все фейки отвечают OK, результат соответствует ожиданиям.
2. **Отмена контекста** — `ctx, cancel := context.WithCancel(...)`;
   `cancel()` до старта или в середине; ожидаем `context.Canceled` или
   `ErrCanceled`.
3. **Ошибки адаптеров** — подменяем фейк так, чтобы возвращал ошибку
   (например, `ErrTapeFull`); проверяем, что use case её пробрасывает и
   корректно оборачивает.
4. **Типизированные ошибки** — `errors.Is(err, domain.ErrXxx)` истинно.
5. **Побочные эффекты** — на фейках проверяем состояние (например, сколько
   блоков записано, какие файлы в Catalog).

## 6. Чего мы не делаем

- Не используем mock-генераторы (`mockery`, `gomock`). Фейки пишутся руками,
  потому что они простые и нам их немного (один на каждый порт).
- Не тестируем приватные функции через `export_test.go` без необходимости.
  Если приватная функция сложная — выносим в отдельный тип или в `domain`.
- Не гоняем интеграционные тесты на каждой правке: они живут в отдельном
  `test/integration/`, запускаются в CI.
- Не делаем снапшот-тесты UI. UI тонкий и меняется часто; smoke-test
  вручную.

## 7. Пример теста (эталонный стиль)

```go
package scan_test

import (
    "context"
    "errors"
    "testing"

    "lentovodec/internal/domain"
    "lentovodec/internal/testutil"
    "lentovodec/internal/usecase/scan"
)

func TestScanner_AppendDetectsAddedAndModified(t *testing.T) {
    cases := []struct{
        name    string
        fs      map[string]string
        before  map[string]domain.FileMeta
        want    []domain.FileState
    }{
        {
            name: "new file",
            fs:   map[string]string{"/etc/hosts": "x"},
            before: nil,
            want: []domain.FileState{domain.StateAdded},
        },
        {
            name: "modified file",
            fs:   map[string]string{"/etc/hosts": "changed"},
            before: map[string]domain.FileMeta{
                "/etc/hosts": {Path: "/etc/hosts", Size: 1, Hash: "old"},
            },
            want: []domain.FileState{domain.StateModified},
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            fs := testutil.NewMapFS(tc.fs)
            cat := testutil.NewMemCatalog().WithSnapshot(tc.before)
            s := scan.New(cat, testutil.NoProgress{}, testutil.NoopLogger())

            got, err := s.Scan(context.Background(), domain.Job{
                Mode:  domain.ModeAppend,
                Paths: []string{"/etc"},
            })
            if err != nil { t.Fatalf("scan: %v", err) }
            if len(got) != len(tc.want) { t.Fatalf("got %d, want %d", len(got), len(tc.want)) }
            for i, want := range tc.want {
                if got[i].State != want {
                    t.Errorf("file %d: state=%s, want %s", i, got[i].State, want)
                }
            }
        })
    }
}
```

`Scan` тестируется без `os`, без `archive/tar`, без SQLite. 100% покрытия —
естественный результат.
