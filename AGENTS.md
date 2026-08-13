# AGENTS.md

Каноническая документация проекта — в `docs/`. Этот файл — короткая
шпаргалка по командам и соглашениям для agentic-инструментов.

## Документация (обязательно к прочтению перед правками)

1. `docs/ARCHITECTURE.md` — слои, правила импортов, потоки управления.
2. `docs/SPECIFICATION.md` — функциональные требования, CLI, REST API, схема БД.
3. `docs/FORMAT.md` — двоичный формат ленты.
4. `docs/ROADMAP.md` — план по этапам.
5. `docs/TESTING.md` — стратегия тестирования.
6. `docs/LEGACY_REFERENCE.md` — что брать / не брать из `legacy/nil-backup/`.

## Главные правила

- **Слои:** `domain` → только stdlib; `usecase` → `port`/`domain`;
  `adapter` → реализует `port`; `iface` (cli/web) → тонкая доставка.
- **Запрещено** импортировать `os`/`syscall`/`net`/`golang.org/x/sys` в
  `internal/domain/**` и `internal/usecase/**` (проверка `depguard`).
- **Запрещено** импортировать `internal/adapter/**` из
  `domain`/`usecase`/`port` (проверка `depguard`).
- Время — только через `port.Clock`; случайность — через `port.Rand`.
- Ошибки возвращаем, не логируем в месте создания. Типизированные ошибки
  — в `internal/domain/errors.go`, сравнение через `errors.Is`/`As`.
- `panic` разрешён только в `cmd/lentovodec/main.go`.
- Никаких package-level `var`, кроме `cmd/lentovodec.Version`.

## Команды

| Команда                | Назначение                                        |
| ---------------------- | ------------------------------------------------- |
| `make lint`            | `golangci-lint run ./...` (строгий конфиг)        |
| `make vet`             | `go vet ./...`                                    |
| `make test`            | `go test ./...`                                   |
| `make test-race`       | `go test -race ./...`                             |
| `make cover`           | покрытие, итоговая строка                         |
| `make build`           | `go build -o bin/lentovodec ./cmd/lentovodec`     |
| `make build-tape`      | сборка с тегом `tape` (включает `adapter/linuxtape`) |
| `make web-build`       | Vite-сборка Vue UI в `internal/iface/web/assets/` |
| `make web-dev`         | dev-сервер Vite с прокси на `:8080`               |
| `make clean`           | удалить `bin/`, `coverage/`, web-бандл            |

`linuxtape` и тесты в `test/hardware/` собираются только с build tag
`tape` (`go build -tags tape ./...`).

## Coverage-цели (docs/TESTING.md §4)

| Пакет                            | Цель  |
| -------------------------------- | ----- |
| `internal/domain/**`             | 100%  |
| `internal/usecase/**`            | ≥95%  |
| `internal/adapter/tapeformat`    | 100%  |
| `internal/adapter/{osfs,sqlite,tomlconfig}` | ≥90% |
| `internal/adapter/filetape`      | ≥95%  |
| `internal/iface/{cli,web}`       | 60-80%|

## Зависимости (закреплены в README.md)

`modernc.org/sqlite`, `go-chi/chi/v5`, `spf13/cobra`, `spf13/viper`,
`google/uuid`, `cespare/xxhash/v2`, `bmatcuk/doublestar/v4`,
`log/slog` (stdlib). Перед добавлением новой зависимости — обсудить.
