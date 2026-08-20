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

# REST API демона

Базовый путь `/api`; формат — JSON. Ошибки — `{"error": "...", "code":
"..."}`. REST API — единственный интерфейс демона: Web UI и daemon-команды
CLI построены поверх него.

---

## Аутентификация

Все эндпоинты, кроме `GET /api/status` и `POST /api/auth/login`, требуют
один из двух способов:

| Сценарий | Способ | Кто |
|---|---|---|
| Web UI, CLI daemon-команды | `Authorization: Bearer <session-id>` | Пользователь, залогиненный через `/api/auth/login` |
| Скрипты, curl, мониторинг | `X-API-Key: <api_key из TOML>` | Программный доступ |

- Неаутентифицированный запрос → `401 {"error": "unauthorized", "code":
  "auth_required"}`.
- Сессия — случайные 256 бит из `crypto/rand`, живёт в памяти демона
  (in-memory map с TTL `session_ttl` из конфига, по умолчанию 72h, лимит
  1024). Без JWT и подписей: ревокация всего и вся = перезапуск демона.
- API-ключ сравнивается за постоянное время (`crypto/subtle`); пустой
  `api_key` в TOML — ключ отключён.
- Header-based аутентификация выбрана вместо cookie сознательно: CSRF
  исчезает как класс (сторонняя страница не может прикрепить кастомный
  заголовок).
- **Rate-limit** на `/api/auth/login`: 5 попыток / 30 с на IP, свыше —
  `429`; счётчик сбрасывается при успешном входе.
- События login (успех/провал, IP, имя) пишутся в аудит-лог (`slog`,
  поле `event=auth`); создание/удаление задач и форматирование ленты
  логируются с именем пользователя сессии.

---

## Эндпоинты

### Аутентификация

| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/auth/login` | `{username, password}` → `{token, expires_at}`; rate-limit 5/30 c на IP |
| `POST` | `/api/auth/logout` | Удалить текущую сессию |

### Состояние и конфиг

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/status` | Healthcheck: версия, есть ли лента (probe open+close). Устройство, занятое фоновой задачей или ленточной операцией, считается доступным — оно открыто владельцем |
| `GET` | `/api/config` | Текущий конфиг (TOML как текст) |
| `GET` | `/api/settings` | Текущие настройки устройства |
| `POST` | `/api/settings` | Обновить путь `device`; при активной задаче — 409 |

### Файловая система

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/fs/list?path=/abs` | Список непосредственного содержимого каталога ФС сервера |

Ответ имеет вид:

```json
{
  "path": "/tank/data",
  "parent": "/tank",
  "entries": [{"name": "media", "is_dir": true, "size": 0, "mtime": 0}]
}
```

Путь читается от имени демона и защищён той же Bearer/API-key
аутентификацией, что и остальные API-методы: доступ к дереву равен доступу
администратора. Каталоги идут перед файлами, скрытые элементы не скрываются.
404 `not_found` означает отсутствующий путь, 403 `forbidden` — отсутствие
прав, 400 `bad_request` — путь к файлу вместо каталога.

### Лента

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/tape/info` | Прочитать `TapeLabel` текущей кассеты |
| `POST` | `/api/tape/eject` | Извлечь кассету |
| `POST` | `/api/tape/format?name=&force=` | Форматировать кассету |

Доступ к стримеру сериализован (драйвер st допускает один открытый
дескриптор): во время фоновой задачи бекапа/восстановления ленточные
операции возвращают `409 {"code": "task_running"}` сразу, а не ждут
часы. Привод без кассеты (ENOMEDIUM) — `409 {"error": "нет кассеты в
приводе", "code": "no_medium"}` вместо сырого errno.

### Задания

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/jobs` | Список заданий из TOML |
| `POST` | `/api/jobs` | Добавить задание в TOML |
| `DELETE` | `/api/jobs/{name}` | Удалить задание из TOML |

### Фоновые задачи

| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/backup/start?job=&full=&verify=` | Запустить бекап, вернуть `taskID`; `verify=true` — перечитать записанное со сверкой хешей (фаза `verify` в прогрессе) |
| `POST` | `/api/restore/start?paths=&dest=&original=` | Запустить восстановление, вернуть `taskID` |
| `GET` | `/api/tasks/active` | Список активных задач (включая ожидающие кассету) |
| `GET` | `/api/tasks/{id}/progress` | Прогресс задачи (объект ниже) |
| `POST` | `/api/tasks/{id}/continue` | Продолжить задачу в `awaiting_tape`: тело `{"tape_name": "..."}` (пустое/отсутствующее — предложенное имя); 409 — задача не в ожидании, 404 — задачи нет. Событие пишется в аудит-лог (`event=task_continue`, пользователь сессии или `api-key`) |

Одновременно активна **одна** задача (стример один): повторный запуск до
завершения предыдущей — ошибка «занято». Ожидающая кассету задача
(`awaiting_tape`) считается активной. Реестр in-memory: перезапуск
демона теряет реестр задач, но не данные.

Прогресс-объект:

```json
{
  "id": "task-abcdef12",
  "state": "running",
  "phase": "write",
  "current_file": "/path/to/file",
  "processed_bytes": 1234567,
  "total_bytes": 9876543,
  "percent": 12.5,
  "speed_mbps": 145.2,
  "logs": ["...последние 50 строк..."],
  "error": "",
  "message": "",
  "suggested_tape_name": ""
}
```

`state` — `running | awaiting_tape | success | error`; `phase` —
`scan | write | verify | finalize`; `logs` — кольцевой буфер последних
строк лога задачи.

`awaiting_tape` — расширение spanning: задача приостановлена, нужна
следующая кассета цепочки. `message` несёт текст для оператора
(например, «кассета test-tape закрыта (span): вставьте чистую кассету
media-002 (часть 2)»), `suggested_tape_name` — предзаполнение имени
в диалоге (пустое `tape_name` в continue примет его). После успешного
continue задача возвращается в `running`. Отменить ожидающую задачу
можно остановкой демона (cancel-эндпоинта нет).

### Каталог

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/catalog/tapes` | Список кассет |
| `GET` | `/api/catalog/sessions?tape=` | Список сессий (фильтр по UUID кассеты); объект сессии включает `part` — номер части в цепочке spanning-запуска (обычная сессия — `1`) |
| `GET` | `/api/catalog/sessions/{id}/files` | Файлы сессии |
| `GET` | `/api/catalog/search?q=` | Поиск файлов по подстроке |
| `DELETE` | `/api/catalog/sessions/{id}` | Удалить сессию из каталога |
| `POST` | `/api/catalog/prune?days=` | Удалить сессии старше N дней |

### Статические ассеты

`GET /` и всё, что не матчит `/api/*`, отдаёт встроенный `embed.FS` с
собранным Vue-бандлом (SPA-fallback, content-type по расширению).

---

## Примеры

### Логин и запуск бекапа

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:29201/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret"}' | jq -r .token)

TASK=$(curl -s -X POST \
  "http://127.0.0.1:29201/api/backup/start?job=media" \
  -H "Authorization: Bearer $TOKEN" | jq -r .task_id)

curl -s http://127.0.0.1:29201/api/tasks/$TASK/progress \
  -H "Authorization: Bearer $TOKEN" | jq
```

### То же через `X-API-Key` (для скриптов)

```bash
curl -s http://127.0.0.1:29201/api/catalog/tapes \
  -H "X-API-Key: $LENTOVODEC_API_KEY"
```

### Смена устройства

```bash
curl -s -X POST http://127.0.0.1:29201/api/settings \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"device": "/dev/nst1"}'
```

### Продолжение задачи после смены кассеты (spanning)

```bash
# Прогресс показывает state=awaiting_tape, message и suggested_tape_name
curl -s -X POST http://127.0.0.1:29201/api/tasks/$TASK/continue \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"tape_name": "media-002"}'   # или {} — примет предложенное имя
```

---

Семантика бекапа/восстановления — [features.md](features.md); CLI —
[cli.md](cli.md); развёртывание и сетевой доступ — [os-setup.md](os-setup.md).
