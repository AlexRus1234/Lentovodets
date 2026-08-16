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

# Двоичный формат ленты

Канон физического представления данных на LTO-ленте. **Любой код,
работающий с лентой, обязан соответствовать этому документу.**

## 1. Обзор

Лента — это последовательность **блоков фиксированного размера** + special
**filemark'и** (EOF-маркеры, записываемые командой `MTWEOF`).

Каждая логическая единица (ярлык, индекс сессии, tar-поток сессии) — это
один или несколько блоков, завершаемых filemark'ом. Между filemark'ами
логические позиции нумеруются начиная с 1.

## 2. Константы

| Имя              | Значение                       | Описание                          |
| ---------------- | ------------------------------ | --------------------------------- |
| `Magic`          | `"LENTOVODEC_TAPE_V2"`         | Строковая сигнатура в ярлыке      |
| `FormatVersion`  | `2`                            | Версия формата                    |
| `BlockSize`      | `256 * 1024` (256 KiB)         | Размер блока на ленте             |
| `IndexPadding`   | до `BlockSize`                 | Индекс добивается нулями до кратного `BlockSize` |
| `CopyBuffer`     | `4 * 1024 * 1024` (4 MiB)      | Буфер копирования файлов в tar    |
| `HashAlgo`       | xxhash64, hex (16 символов)    | Контроль целостности файлов       |

`BlockSize = 256 KiB` — типичный для LTO-5..LTO-9 и хорошо ложится в
буфер стримера.

## 3. Старая сигнатура не поддерживается

Кассеты, отформатированные legacy `nil-backup` (`Magic = "NIL_BACKUP_TAPE"`),
**намеренно не читаются**. При попытке прочитать такую кассету
`FormatTapeUseCase.ReadLabel` возвращает `ErrForeignFormat` с указанием
магической строки. Пользователь должен либо явно переформатировать
(`--force`), либо восстановить данные старым бинарем.

## 4. Физическая раскладка

```
              позиция блоков                  filemark №
              ─────────────────              ──────────
   ┌──────────────────────────┐
   │  LABEL  (1 блок 256 KiB) │ ─── EOF ─────────────── 1
   ├──────────────────────────┤
   │  SESSION 1 INDEX         │ ─── EOF ─────────────── 2
   ├──────────────────────────┤
   │  SESSION 1 TAR           │ ─── EOF ─────────────── 3
   ├──────────────────────────┤
   │  SESSION 2 INDEX         │ ─── EOF ─────────────── 4
   ├──────────────────────────┤
   │  SESSION 2 TAR           │ ─── EOF ─────────────── 5
   ├──────────────────────────┤
   │  ...                     │
   ├──────────────────────────┤
   │  SESSION K INDEX         │ ─── EOF ─────────────── 2K
   ├──────────────────────────┤
   │  SESSION K TAR           │ ─── EOF ─────────────── 2K+1
   ├──────────────────────────┤
   │  (пусто, EOD)            │ ─── EOF ─── EOF ─────── 2K+2, 2K+3
   └──────────────────────────┘
```

**Главное отличие от legacy:** между индексом и tar каждой сессии **всегда**
ставится filemark, и после tar'а тоже. Поэтому на ленте с `K` сессиями
ровно `2K + 1 + 2` filemark'а: один после ярлыка, по два на каждую сессию,
и двойной в конце = EOD.

> В legacy `WriteBackupToTape` не вызывал `MTWEOF` после сессий — это была
> причина хрупкости умного восстановления. Здесь это исправлено.

## 5. `TapeLabel` JSON

Первый блок ленты. JSON, добитый нулями до `BlockSize`.

```json
{
  "magic":          "LENTOVODEC_TAPE_V2",
  "format_version": 2,
  "name":           "media-001",
  "uuid":           "550e8400-e29b-41d4-a716-446655440000",
  "formatted_at":   "2026-08-13T12:34:56Z"
}
```

| Поле             | Тип    | Описание                                                |
| ---------------- | ------- | ------------------------------------------------------- |
| `magic`          | string  | Константа `Magic`. Если не совпадает — `ErrForeignFormat` или `ErrBlankTape`. |
| `format_version` | int     | `2`. Если `> current` → `ErrNewerFormat`.                |
| `name`           | string  | Человекочитаемое имя кассеты, уникально в каталоге.     |
| `uuid`           | string  | RFC-4122 v4, канонический вид (8-4-4-4-12, lowercase).  |
| `formatted_at`   | string  | RFC-3339, UTC (`time.RFC3339`).                          |

## 6. SessionIndex JSON

Один или несколько блоков (если JSON длиннее `BlockSize`, лежит в
нескольких блоках; filemark ставится только после последнего). JSON, добитый
нулями до кратного `BlockSize`.

```json
{
  "format_version":  2,
  "session_num":     3,
  "type":            "INC",
  "job_run_id":      "abc123def456...",
  "timestamp":       1691928896,
  "job_name":        "media",
  "files": [
    {
      "path":     "/tank/data/media/movie.mkv",
      "size":     12345678,
      "mod_time": 1691000000,
      "is_dir":   false,
      "hash":     "0123456789abcdef",
      "state":    "M"
    },
    {
      "path":     "/tank/data/media/deleted-folder",
      "size":     0,
      "mod_time": 0,
      "is_dir":   true,
      "hash":     "",
      "state":    "D"
    }
  ]
}
```

`state = "D"` (tombstone) — файл **не записан в tar-поток**. Восстановление
mirror-режима интерпретирует это как «удалить файл в целевом каталоге».

## 7. TAR-поток

Стандартный `archive/tar` (GNU или PAX формат — зафиксировать на GNU в impl).
Пишется в блочный writer ленты. Завершается filemark'ом.

Конвенции header'ов:

| Поле          | Значение                                                 |
| ------------- | -------------------------------------------------------- |
| `Name`        | `filepath.Clean(fm.Path)` — без префикса `./`            |
| `Typeflag`    | `TypeDir` для каталогов, `TypeReg` для файлов            |
| `Size`        | `fm.Size`                                                |
| `ModTime`     | Из `fm.ModTime`                                          |
| `Mode`        | Из `FileInfo` (только при бекапе; при restore игнорируется, если `--preserve-mode` не задан) |
| `Uname`/`Gname` | Не записываем                                          |

Tombstone'ы (`state == "D"`) **не кладутся в tar**, только в индекс.

## 8. Целостность

- Для каждого файла в индексе хранится `hash = hex(xxhash64(содержимое))`.
- При любом restore (`Full`, `Selective`, `Smart`) файл считается
  успешно прочитанным **только если** его xxhash совпал.
- При восстановлении mirror-режима: если в каталоге целевой папки
  существует файл, для которого в текущей сессии есть tombstone, он
  удаляется (после успешного завершения restore).

## 9. Правила перемотки

Команда `MTFSF(N)` пропускает `N` filemark'ов вперёд, останавливаясь сразу
после filemark'а № N (на начале следующей записи).

| Что нужно сделать                      | Команда                          |
| -------------------------------------- | -------------------------------- |
| Перейти к началу ярлыка                | `MTREW`                          |
| Перейти к началу индекса сессии K      | `MTFSF(2*K - 1)`                 |
| Перейти к началу tar сессии K          | `MTFSF(2*K)`                     |
| Перейти в конец данных (для append)    | `MTEOM` (или поиск двойного EOF) |
| Записать новую сессию K+1 (append)     | `MTEOM`, затем INDEX, `MTWEOF`, TAR, `MTWEOF` |
| Перезаписать сессию K (только FULL)    | `MTREW`, затем сессии 1..K       |

> Старый формат был недетерминирован из-за отсутствия filemark'ов: `MTFSF`
> перепрыгивал сразу через несколько сессий. Здесь это невозможная ситуация.

## 10. Append vs Full

- **Full backup** (`opts.Full == true` или первая сессия на ленте):
  перемотка в `MTREW`, перезапись ярлыка **не** происходит (он сохраняется),
  но все последующие сессии затираются новыми. `session_num = 1`, `type = FULL`.
  Рекомендуется для случая «начинаем новую цепочку инкрементов».

- **Incremental** (по умолчанию): `MTEOM`, дозапись новой сессии после
  последней. `session_num = last + 1`, `type = INC`.

В обоих случаях формат одной сессии идентичен (INDEX + TAR + 2 filemark'а).

## 11. Версионирование и совместимость

- Любое изменение формата инкрементит `FormatVersion` в `TapeLabel`.
- При чтении лейбла проверяется `format_version`: если равен текущему —
  OK; если меньше — поддерживается обратная совместимость (с предупреждением
  в логе); если больше — `ErrNewerFormat`, операция отменяется.
- `magic` — признак семейства форматов. Если не `"LENTOVODEC_TAPE_V2"` и не
  начинается с `"LENTOVODEC_TAPE_"` — `ErrForeignFormat`.
