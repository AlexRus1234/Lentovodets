# Legacy Reference

Старая реализация лежит в `nil-backup/` (этап 0 — `legacy/nil-backup/`).
Из нового кода **не импортируется**. Используется только как:

1. Источник референса при проектировании (что уже работало).
2. Чек-лист багов, которые **нельзя повторить** в новом коде.

Все пути ниже — от корня репозитория. Если ссылка не работает, значит
Этап 0 ещё не выполнен: замените `legacy/nil-backup/` на `nil-backup/`.

## 1. Карта файлов

| Legacy файл                             | Что брать                                           | Что не брать                                |
| --------------------------------------- | --------------------------------------------------- | ------------------------------------------- |
| `nil-backup/engine/catalog.go`          | SQL-схему (с доработками, см. SPECIFICATION §3.1), пакетные INSERT'ы в транзакции | возврат `map[string]interface{}` вместо типизированных структур; игнор ошибок `db.Exec("PRAGMA ...")` |
| `nil-backup/engine/label.go`            | Идею `Magic`/`Name`/`UUID`/`FormattedAt` ярлыка     | самописный `generateUUID` (заменить на `google/uuid`); `Magic = "NIL_BACKUP_TAPE"` (заменить на `"LENTOVODEC_TAPE_V2"`) |
| `nil-backup/engine/scanner.go`          | Сканирование с xxhash, сравнение size+mtime        | `matchesExclude` через `strings.Contains` (заменить на glob); отсутствие `mirror`-логики; жёстко зашитые имена `nil_backup.db`/`nil.log` |
| `nil-backup/engine/tape.go`             | Идею «индекс + tar», блочный I/O 256 KiB, прогресс-трекер | отсутствие `MTWEOF` между сессиями; `os.Stat` в логике записи (через порт); глобал `TapeDevice`; повторное `os.OpenFile` после индекса |
| `nil-backup/engine/tape_ctrl.go`        | Универсальный `sendTapeCommand(fd, op, count)` для ioctl | попадание ioctl в бизнес-код (должно быть только в `adapter/linuxtape`) |
| `nil-backup/engine/restore_tape.go`     | Идею `RestoreFromTape` с проверкой xxhash через `io.TeeReader` | обработку session boundaries, основанную на «волшебных» reopen'ах |
| `nil-backup/engine/restore_tar.go`      | Идею прерывания чтения, когда все пути найдены      | пропуск проверки xxhash (в новом — обязательна) |
| `nil-backup/engine/restore_smart.go`    | Идею перебора копий из каталога до первой здоровой  | формулу `TapeForwardSpaceFile(sessionNum*2)` — пересчитана под новый формат |
| `nil-backup/cmd/daemon.go`              | Карту REST-эндпоинтов (с дополнениями, см. SPECIFICATION §6) | самописный SSI через строковые замены (в новом — статические ассеты из embed); молчаливое `_ = err` во всех обработчиках |
| `nil-backup/cmd/backup.go`              | Локальную структуру `JobConfig` (как референс полей) | **дублирование** — в новом коде `Job` живёт один раз в `internal/domain` |
| `nil-backup/cmd/restore.go`             | Семантику флагов `--dest`, `--original`, `--paths`  |                                             |
| `nil-backup/cmd/jobs.go`                | Валидацию `Mode`                                    | прямую запись TOML через viper без транзакционности (в новом — через `tomlconfig` адаптер) |
| `nil-backup/client/api.go`              | Структуру HTTP-клиента для daemon-команд            |                                             |
| `nil-backup/web/index.html`, `app.js`, `components/` | Структуру экранов Tape/Jobs/Catalog/Files | Vue с CDN без сборки (в новом — Vite + типы) |
| `nil-backup/web/i18n.js`                | Словарь ru/en как стартовую точку                   |                                             |

`nil-backup/engine/progress.go` стоит прочитать ради `ProgressTracker` с
throttle 2 Гц — идея правильная, переносится в `port.ProgressReporter`.

## 2. Что сломано в legacy

Этот список — одновременно спецификация того, что **новый код должен делать
правильно**. Соответствующие тесты — обязательны.

### 2.1. Архитектурные проблемы

- **`mirror` не реализован.** `ScanJob(paths, mode, excludes, catalog, ...)`
  принимает `mode`, но не использует его (см. `nil-backup/engine/scanner.go:31`).
  Поле `State = 'D'` заложено в схему БД и фильтруется в `GetAllFileCopies`
  (`path != 'D'`), но никто его не проставляет.
- **`MTWEOF` между сессиями не пишется.** В `WriteBackupToTape`
  (`nil-backup/engine/tape.go:38-140`) после индекса и после tar'а нет вызова
  `TapeWriteEOF()`. Из-за этого `RestoreFromTape` и `RestorePaths`
  (через `TapeForwardSpaceFile`) работают непредсказуемо.
- **`JobConfig` дублирован** в `nil-backup/cmd/backup.go` и
  `nil-backup/tui/backend.go`. В новом коде `Job` — в `internal/domain/job.go`.
- **Глобал `TapeDevice`** (`nil-backup/engine/tape.go:12`). В новом — через
  конструктор адаптера.
- **Прямой доступ к engine из TUI** (`nil-backup/tui/handler_tape.go`).
  TUI вырезан целиком; в новом CLI/Web лента доступна **только** через use case.

### 2.2. Баги и хрупкость

- **`generateUUID`** в `nil-backup/engine/label.go:21-25` — это
  `hex(8 случайных байт)` = 16 hex-символов, не RFC-4122. В новом —
  `google/uuid` из зависимостей.
- **`matchesExclude`** в `nil-backup/engine/scanner.go:20-29` —
  `strings.Contains`, в комментарии прямо написано «или можно использовать
  `filepath.Match`». В новом — glob + doublestar.
- **`GetLatestSessionNum` игнорируется** в `nil-backup/cmd/backup.go`
  (`lastNum, _ := ...`). В новом — ошибка пробрасывается.
- **`os.Chown`** в `nil-backup/engine/restore_tape.go` и `restore_tar.go`
  требует root; в не-root демоне молча проваливается. В новом — через
  опциональный флаг `--preserve-ownership`, по умолчанию off.
- **Ошибки в демоне глотаются** (`_ =`) во всех HTTP-обработчиках
  (`nil-backup/cmd/daemon.go`). В новом — `errors.Is`/`As`, корректные
  HTTP-коды, JSON-ответ с `code`/`error`.
- **`initConfig` принудительно ставит `SetConfigType("toml")`** — флаг
  `--config` с `.yaml`/`.json` всё равно интерпретируется как TOML.
  В новом — фиксируется, что конфиг только TOML; путь должен кончаться на
  `.toml`.

### 2.3. Неэффективности

- `WriteBackupToTape` дважды открывает `os.OpenFile(TapeDevice, ...)`: один
  раз для индекса, второй для tar. Между ними позиция ленты не должна
  меняться, но это неявно. В новом — один `port.Tape`, методы которого
  сами пишут блоки.
- `GetLatestFileState` вызывается в сканере для **каждого** пути отдельно.
  В новом — `GetLatestFileStates(paths)` одним запросом.

## 3. История из `nil-backup/nil.log`

Журнал `nil-backup/nil.log` содержит реальные прогоны. Что он подтверждает:

- Full и increment-бекапы в марте 2026 завершались успешно.
- Полное восстановление в `./RECOVERY` работало (файл
  `nil-backup/RECOVERY/test_media/avatar.txt` — след).
- Умное восстановление в `/tank/data/restore-test` падало с
  *«КРИТИЧЕСКИЙ ОТКАЗ: Все известные копии файлов повреждены или
  недоступны»*. Это ровно тот сценарий, который мы фиксим правильной
  расстановкой `MTWEOF` и детерминированными перемотками.

Эти данные можно использовать как референс для приёмочных тестов
(`test/hardware/`): формат `LENTOVODEC_TAPE_V2` должен давать стабильный
результат там, где legacy спотыкался.

## 4. Тестовые данные из legacy

В репо есть готовые каталоги для smoke-тестов (после Этапа 0 —
`legacy/nil-backup/`):

| Путь                           | Что внутри                                    |
| ------------------------------ | --------------------------------------------- |
| `test_media/`                  | `avatar.txt` (35 B) + три `.bin` по 300/500/500 MiB для замеров скорости |
| `test_system_1/nginx.conf`     | Конфиг nginx                                  |
| `test_system_2/mysql.log`      | Лог mysql                                     |
| `test_dir/secret_folder/deep_level/pass.txt` | Проверка вложенности и специальных символов |
| `RECOVERY/test_media/avatar.txt` | След успешного восстановления               |

Можно скопировать в `test/integration/testdata/` для воспроизводимости.
