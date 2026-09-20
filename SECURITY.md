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

# Безопасность

Краткая модель угроз. Разбор конкретных механизмов —
[docs/SPECIFICATION.md](docs/SPECIFICATION.md) §6.0/§9.1/§9.2 и
[docs/func/ru/os-setup.md](docs/func/ru/os-setup.md); сообщения об
уязвимостях — через контакты владельца репозитория (не через
публичный issue).

## Поверхность

| Слушатель | Адрес (дефолт) | Доступ | Что открыто |
|---|---|---|---|
| демон | `127.0.0.1:29201` | loopback (дефолт) | REST `/api/*`, Web UI, `/api/fs/list` |

CLI в local-режиме сеть не слушает вовсе. Прямой LAN-доступ —
осознанное решение администратора: bind на внешний интерфейс
**требует** настроенного пароля (отказ старта при не-loopback bind
без `web_password_hash`, включая override флагами `--bind/--port`);
рекомендуемый путь извне — SSH-туннель (SPEC §9.2). HTTP без TLS —
принятый компромисс локальной сети; демон не работает с
сертификатами (нужен TLS — обратный прокси).

## Аутентификация и сессии

- Пароль — bcrypt (лимит ввода 72 байта, bcrypt-ошибки не
  проглатываются); хеш генерируется `lentovodec passwd` и кладётся
  в TOML.
- Сессии — случайные 256 бит (`crypto/rand`), in-memory map с TTL
  (`session_ttl`, дефолт 72h) и лимитом размера (1024); рестарт
  демона отзывает все сессии. JWT не используется.
- API-ключ (`X-API-Key`) сравнивается в постоянном времени
  (`crypto/subtle`); предназначен для скриптов и systemd-timer.
- Rate-limit логина: 5 попыток / 30 c на IP (`429` после), счётчик
  сбрасывается при успехе.
- Аудит: `login_success`/`login_failure` (IP, username) — в общий
  slog-лог с полем `event=auth`; создание/удаление задач и
  форматирование ленты логируются с именем пользователя сессии.
- Header-based токены вместо cookie — CSRF неприменим (токен не
  передаётся автоматически браузером).

## Кассета как недоверенный ввод

- `restore --dest` — песочница: пути с `..` из индекса ленты
  отклоняются, запись через восстановленные symlink запрещена —
  зловредная или повреждённая кассета не пишет файлы за пределы
  каталога назначения. `--original` (перезапись по исходным путям) —
  осознанный риск оператора; в Web UI — подтверждающий диалог.
- JSON-ярлык и индексы сессий валидируются (magic, версия формата,
  RFC-3339 `formatted_at`, лимиты объёма); xxhash64 каждого файла
  сверяется при любом восстановлении — подмена содержимого ловится,
  ошибка — типизированная, не строковая.
- Повреждённые сессии пропускаются честной ошибкой (тип
  `SessionDamageError`), чтение не «лечится» молча; урезанный
  tar-поток детектится по несоответствию с индексом, частичный файл
  не остаётся на диске.
- `catalog rebuild` переносит из кассеты только ярлык и индексы
  (JSON), tar-поток не читается — реконструкция не может записать
  на диск пользовательские данные.

## Привод и ОС (rootless)

- Управление стримером не требует root: `/dev/nst*` — char-устройство
  группы `tape`; критерий доступа — успешное открытие устройства
  (probe), не проверка uid (SPEC §9.1).
- `CAP_SYS_RAWIO` требуется только TapeAlert-диагностике (SG_IO
  LOG SENSE фильтруется ядром на st-нодах); без права — деградация
  в «диагностика недоступна», MTIOCTOP/read/write не зависят.
- Доступ к стримеру в демоне сериализован (tapeGate): параллельные
  операции получают `409`, а не гонку открытых дескрипторов (EBUSY);
  ioctl переживает EINTR повтором.
- Рекомендуемое развёртывание — выделенный пользователь `lentovodec`
  + systemd-харднинг (`ProtectSystem=strict`, `ReadWritePaths`);
  образец юнита и прав на каталоги — os-setup.md.

## Конфиг и секреты

- `web_password_hash` (bcrypt) и `api_key` хранятся в TOML без
  секрет-менеджера — компромисс одномашинного инструмента (SPEC
  §9.2); права на конфиг — `0750` `root:lentovodec` (os-setup.md).
- SQLite-каталог (WAL) — в `/var/lib/lentovodec`; демон и local-команды
  работают от одного пользователя (WAL-файлы рядом с БД).

## Webhook-уведомления

- `webhook_url` из TOML: один исходящий POST с таймаутом
  (`webhook_timeout`), асинхронно, один повтор; сбой доставки не
  влияет на результат бекапа. Адрес — зона доверия администратора:
  SSRF-фильтров нет (инструмент одной машины, URL задаёт владелец).
