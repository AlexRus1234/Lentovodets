# lentovodec

Самописная система резервного копирования на ленточные стримеры LTO
(по умолчанию `/dev/nst0`), с каталогом в SQLite и двумя интерфейсами
управления: CLI и Web.

> **Статус проекта:** полный рерайт с нуля. Старая реализация сохранена как
> референс и находится в `nil-backup/` (после Этапа 0 — в `legacy/nil-backup/`).
> Из нового кода она **не импортируется**; ссылка на неё — только в
> [`docs/LEGACY_REFERENCE.md`](docs/LEGACY_REFERENCE.md).

## Текущий этап

Завершены `Этап 0 — Подготовка инфраструктуры`, `Этап 1 — Domain layer`,
`Этап 2 — Port layer`, `Этап 3 — adapter/tapeformat` (чистый формат
ленты: ярлык, JSON-индекс, tar-поток — с golden-тестами и 100% покрытия),
`Этап 4 — adapter/osfs, sqlite, tomlconfig` (реальная ФС, каталог
SQLite, конфиг TOML с записью заданий; покрытие ≥93% на пакет),
`Этап 5 — adapter/filetape, linuxtape` (лента-как-файл с персистентностью
и ioctls драйвера st; покрытие filetape 96.3%), `Этап 6 — Use cases`
(Scanner, Backup, Restore Full/Selective/Smart, FormatTape, Catalog;
покрытие 95.2–100% на пакет; новые порты `TapeCodec`/`Hasher`),
`Этап 7 — iface/cli, iface/web, cmd/lentovodec` (полный CLI: local- и
daemon-команды, `passwd`; REST API демона на chi с bcrypt-аутентификацией,
сессиями, rate-limit и реестром фоновых задач; Web-раздача из embed.FS;
покрытие cli 75%, web 83%) и `Этап 8 — Web UI (Vue 3 + Vite)` (пять
экранов: Login/Tape/Jobs/Catalog/Files; ru/en без внешних i18n-библиотек;
прогресс задач — поллинг 1 Гц; бандл embed'ится в бинарь).
Следующий — `Этап 9 — Интеграционные и hardware-тесты`.
См. [ROADMAP](docs/ROADMAP.md).

## Быстрый старт (CLI)

```bash
make web-build                    # Web UI → internal/iface/web/assets (нужен Node 22+;
                                  # на fresh clone обязательно до make build — embed)
make build                        # бинарь bin/lentovodec
./bin/lentovodec jobs add media --paths /tank/data --mode mirror
./bin/lentovodec passwd           # bcrypt-хеш → web_password_hash в TOML
./bin/lentovodec tape format LTO-001
./bin/lentovodec backup media
./bin/lentovodec restore --paths /tank/data/a.txt --dest /safe
./bin/lentovodec daemon           # REST API + Web UI на 127.0.0.1:29201
```

> Бандл UI и `web/node_modules/` в git не попадают: после свежего клона
> запускайте `make web-build` перед `make build` (CI делает это сам).

## Web UI

- Прод: `make web-build` собирает Vue-бандл в `internal/iface/web/assets/`,
  откуда он встраивается в бинарь (`//go:embed`) и раздаётся демоном
  на `http://127.0.0.1:29201`. Вход — `web_username`/`web_password_hash`
  из TOML (хеш — `lentovodec passwd`); на loopback-бинде без учётки
  аутентификация выключена.
- Дев: `make web-dev` — Vite на `:5173` с прокси `/api` на демона
  (`:29201`); рядом запустите `lentovodec daemon`.
- Экраны: лента (ярлык/формат/eject/устройство), задания (карточки,
  запуск, прогресс), каталог (сессии с фильтром), файлы сессии
  (навигация, выбор, восстановление). Язык ru/en — переключатель в
  шапке.

Команды `catalog *`, `tape info/eject` работают через демона
(`--server`, по умолчанию `http://127.0.0.1:29201`); авторизация —
`api_key` из TOML либо логин/пароль. Остальные команды — прямой доступ
к ленте (rootless, группа `tape`; детали — SPEC §9.1).

## Документация (канон)

Порядок чтения для нового исполнителя:

1. **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — слои, правила, потоки
   управления. Что можно, что нельзя. С этого файла начинать.
2. **[docs/SPECIFICATION.md](docs/SPECIFICATION.md)** — функциональные
   требования: сущности, сценарии, CLI, REST API, Web UI, схема БД.
3. **[docs/FORMAT.md](docs/FORMAT.md)** — двоичный формат ленты (физическая
   раскладка, JSON-схемы ярлыка и индекса, правила расстановки filemark'ов).
4. **[docs/ROADMAP.md](docs/ROADMAP.md)** — план по этапам: что создаётся, какие
   тесты, критерий готовости каждого этапа.
5. **[docs/TESTING.md](docs/TESTING.md)** — стратегия тестирования, test doubles,
   целевые показатели coverage.
6. **[docs/LEGACY_REFERENCE.md](docs/LEGACY_REFERENCE.md)** — что взять за
   референс из старого кода и что именно в нём сломано.

## Зафиксированные технологические решения

| Параметр             | Значение                                  |
| -------------------- | ----------------------------------------- |
| Имя модуля и бинаря  | `lentovodec`                              |
| Язык                 | Go 1.26+                                  |
| Архитектура          | Hexagonal (Ports & Adapters)              |
| Логирование          | `log/slog` (стандартная библиотека)       |
| Линтер               | `golangci-lint` (строгая конфигурация)    |
| База данных          | SQLite через `modernc.org/sqlite` (без CGO) |
| HTTP-роутер          | `go-chi/chi/v5`                           |
| CLI                  | `spf13/cobra` + `spf13/viper`             |
| Web UI               | Vue 3 + Vite, бандлится в `embed.FS`      |
| Интерфейсы           | только CLI и Web (TUI вырезан)            |
| Web-доступ           | логин/пароль (bcrypt), сессии в памяти; bind по умолчанию `127.0.0.1:29201` (LAN — только осознанно, см. SPEC §9.2) |
| Совместимость        | нет; новый формат ленты `LENTOVODEC_TAPE_V2`, старые кассеты не читаются |

## Главное архитектурное правило

> **Бизнес-логика (`internal/domain`, `internal/usecase`) не имеет права
> импортировать `os`, `syscall`, `ioctl` и любые конкретные адаптеры.** Весь
> ввод-вывод — через интерфейсы из `internal/port`. Это правило проверяется
> линтером `depguard` и является обязательным.
