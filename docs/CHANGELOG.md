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
