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

### Added

- Бекап spanning в local-режиме: сессия, не влезающая в кассету
  (планировщик `capacity`/`min_tail`), пишется частями на несколько
  кассет — с интерактивной сменой: «Кассета X закрыта. Вставьте
  чистую кассету, имя [Y]:» (Enter принимает предложенное — инкремент
  числового суффикса). Для неинтерактивного запуска — флаг
  `--next-tape NAME`; каждая новая кассета форматируется и
  регистрируется автоматически. Заполнение кассеты посреди части
  (`TapeFullError`) переносит часть целиком на следующую кассету
  с восстановлением EOD заполненной; две подряд неудачные попытки
  с полной ёмкостью — ошибка с рекомендацией уменьшить `capacity`.
  Вывод `backup` дополняют строки «частей N на кассетах: …».
- Каталог: сессия знает номер части в цепочке spanning-запуска
  (`part`, обычная сессия — часть 1). Существующие БД мигрируются
  автоматически (`PRAGMA user_version` 0→1, старые строки получают
  `part = 1`); REST `/api/catalog/sessions` отдаёт поле `part`,
  `catalog sessions` показывает суффикс `part N` у частей >1. Части
  одного запуска связывает существующий `job_run_id`; сама нарезка
  бекапа на несколько кассет — в разработке.
- Формат ленты: задел под spanning — индекс сессии может нести номер
  части (`part`) и обратную ссылку на предыдущую кассету цепочки
  (`continues`), после закрывающей пары filemark'ов возможен
  JSON-блок-указатель продолжения на следующую кассету. Изменения
  аддитивные: старый бинарь игнорирует новые поля, новый читает старые
  ленты; `restore full`/`tape readtest` на кассете с указателем
  останавливаются с предупреждением (следование по цепочке — в
  разработке).
- Конфиг `capacity`/`min_tail` (TOML/env): оценка ёмкости кассеты и
  порог остатка для планировщика частей. При заданном `capacity` файл
  больше кассеты (или её остатка) — честная ошибка после скана, до
  записи на ленту; в логе `backup planned` — `planned_parts` и
  `budget_bytes`.

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
