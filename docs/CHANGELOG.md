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

# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/),
версионирование — SemVer. Подробности по этапам — заметки в
[ROADMAP.md](ROADMAP.md); здесь фиксируются пользовательские изменения
(CLI/API/формат/конфиг) между релизами.

## [Unreleased]

### Fixed

- Статистика бекапа (`Stats.Bytes`) больше не включает Lstat-размеры
  каталогов (на Linux они ≠ 0 и платформозависимы); размер каталога в
  индексе — всегда 0, изменения каталога детектятся по mtime.
- `restore --dest` корректно переносит пути с именем тома (`C:/data`)
  при восстановлении на ОС, отличной от ОС бекапа.
- Драйвер `linuxtape`: mt-команды (rewind/eject/format и др.) больше не
  падают с `EINTR` от случайного сигнала (в т.ч. SIGURG от планировщика
  Go) — ioctl повторяется; прерывание возможно только до старта команды,
  повтор безопасен.

## [1.0.0] — 2026-08-16

Первый релиз: завершены все этапы 0–10.

- Формат ленты `LENTOVODEC_TAPE_V2`: JSON-ярлык, сессии
  «JSON-индекс + tar» с filemark-инвариантами, xxhash64 каждого файла
  (см. [FORMAT.md](FORMAT.md)). Кассеты legacy не читаются.
- CLI: `backup`, `restore` (full/selective/smart), `tape
  format/readtest/info/eject`, `jobs list/add/remove`, `catalog
  tapes/sessions/files/search/rm/prune`, `passwd`, `daemon`.
- Демон: REST API (SPEC §6) + Web UI (Vue 3, `embed.FS`); bcrypt-логин,
  сессии с TTL, rate-limit, api_key; bind по умолчанию `127.0.0.1:29201`.
- Rootless-модель: доступ к стримеру через группу `tape`, без root
  (SPEC §9.1); развёртывание — README.
- Каталог SQLite (WAL); режимы заданий `append`/`mirror` (tombstone'ы).

[Unreleased]: https://git.yadr00.internal/AlexRus1234/Lentovodets/compare/v1.0.0...HEAD
[1.0.0]: https://git.yadr00.internal/AlexRus1234/Lentovodets/releases/tag/v1.0.0
