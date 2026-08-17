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

# Справочник CLI

---

## Глобальные флаги

Наследуются всеми подкомандами:

| Флаг | По умолчанию | Описание |
|---|---|---|
| `--config PATH` | `./lentovodec.toml` | Путь к конфигу |
| `--device PATH` | `/dev/nst0` | Устройство ленты (см. ниже) |
| `--db PATH` | `./lentovodec.db` | Путь к SQLite-каталогу |
| `--log PATH` | `./lentovodec.log` | Файл лога |
| `--server URL` | `http://127.0.0.1:29201` | Адрес демона для daemon-команд |
| `-v`, `--verbose` | — | Отладочный лог |

### Выбор устройства ленты

Сборка с тегом `tape` (Linux) выбирает реализацию по типу пути:

| `--device` | Реализация |
|---|---|
| Character-устройство (`/dev/nst0`) | Реальный стример (`linuxtape`, ioctl `MTIOCTOP`) |
| Существующий обычный файл | Эмулятор ленты `filetape` |
| Несуществующий путь вне `/dev` | Создаётся новая файловая лента |
| Несуществующий путь под `/dev` | Ошибка probe (стример не подключён) |

Сборка без тега `tape` всегда использует `filetape` — запуск на
Windows/macOS и в CI не требует железа.

---

## Режимы команд

| Режим | Как работает |
|---|---|
| `local` | Прямой доступ к ленте и каталогу; демон не нужен |
| `daemon` | Команда идёт в HTTP API (`--server`): `api_key` из TOML, при 401 — интерактивный логин и Bearer-токен |
| `server` | Сам слушает HTTP (`daemon`) |

Граница режимов — явная: local-команды требуют физического доступа к
стримеру с той же машины, daemon-команды — запущенного демона.

---

## Команды

### Резервное копирование

```bash
lentovodec backup <job> [--full] [--dry-run]
```

| Флаг | Описание |
|---|---|
| `--full` | Полный бекап: перезапись цепочки с сессии 1 (старые сессии чистятся из каталога) |
| `--dry-run` | Только сканирование и статистика, без записи |

Пример:

```console
$ lentovodec backup media
сессия #4 INC (id 12) на кассете 550e8400-e29b-41d4-a716-446655440000
изменено 37: добавлено 30, изменено 5, удалено 2; к записи 1.2 GiB
```

Первая сессия кассеты всегда FULL (флаг не нужен). При ошибке записи
созданная сессия удаляется из каталога (компенсация), индекс и лента
остаются согласованными.

### Восстановление

```bash
lentovodec restore [--paths p1,p2] [--dest DIR] [--original]
```

| Флаг | Описание |
|---|---|
| `--paths` | Пути через запятую → smart-восстановление (свежая копия, fallback на старые); пусто — полное восстановление всей кассеты |
| `--dest` | Восстановить в указанный каталог, а не по исходным путям |
| `--original` | Восстановить по исходным путям из индекса (игнорирует `--dest`) |

Примеры:

```bash
lentovodec restore --dest /safe                    # вся кассета в /safe
lentovodec restore --paths /tank/media/a.mkv --dest /safe   # smart по пути
lentovodec restore --paths /tank/media/dir --dest /safe     # smart по поддереву
lentovodec restore --original                      # по исходным путям (перезапись!)
```

Selective-восстановление из конкретной сессии доступно в Web UI (браузер
файлов сессии → «Восстановить выбранное»).

### Лента

| Команда | Режим | Описание |
|---|---|---|
| `lentovodec tape format <name> [--force]` | local | Отформатировать кассету: ярлык (UUID, имя) + двойной EOF + запись в каталог. `--force` — перезапись уже размеченной |
| `lentovodec tape readtest` | local | Диагностическое чтение всей кассеты со сверкой хешей, без записи на ФС |
| `lentovodec tape info` | daemon | Прочитать ярлык текущей кассеты |
| `lentovodec tape eject` | daemon | Извлечь кассету (MTOFFL) |

### Задания

| Команда | Описание |
|---|---|
| `lentovodec jobs list` | Показать задания из TOML |
| `lentovodec jobs add <name> --paths P1,P2 [--mode M] [--desc D] [--exclude E1,E2]` | Добавить задание в TOML |
| `lentovodec jobs remove <name>` | Удалить задание |

`--mode`: `append` (по умолчанию) или `mirror`; семантика режимов и
exclude-шаблонов — [features.md](features.md).

### Каталог (daemon)

| Команда | Описание |
|---|---|
| `lentovodec catalog tapes` | Список кассет |
| `lentovodec catalog sessions [--tape UUID]` | Список сессий (с фильтром по кассете); у частей spanning-цепочек — суффикс `part N` |
| `lentovodec catalog files --session N` | Файлы сессии |
| `lentovodec catalog search <pattern>` | Поиск файлов по подстроке |
| `lentovodec catalog rm --session N` | Удалить сессию из каталога (данные на ленте остаются) |
| `lentovodec catalog prune --days N` | Удалить сессии старше N дней |

### Прочее

| Команда | Режим | Описание |
|---|---|---|
| `lentovodec passwd` | local | Спросить пароль (дважды) и вывести bcrypt-хеш для `web_password_hash` в TOML |
| `lentovodec daemon [--port N] [--bind ADDR]` | server | Запустить HTTP-демона (REST API + Web UI) |

Флаги `--port`/`--bind` переопределяют соответствующую часть `bind` из
TOML (не-loopback адрес требует настроенного пароля — иначе отказ
старта).

---

## Типичные сценарии

```bash
# Новая кассета → первый полный бекап → пара инкрементов:
lentovodec tape format LTO-001
lentovodec backup media                # сессия 1, FULL
lentovodec backup media                # сессия 2, INC
lentovodec backup media --full         # новая цепочка: снова 1, FULL

# Проверка носителя перед закладкой кассеты в архив:
lentovodec tape readtest

# Аварийное восстановление после потери ФС:
lentovodec restore --dest /restore

# Один файл в безопасное место:
lentovodec restore --paths /tank/media/movie.mkv --dest /safe
```

REST API демона — [api.md](api.md); развёртывание — [os-setup.md](os-setup.md).
