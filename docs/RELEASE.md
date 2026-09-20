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

# Чеклист релиза

Ручные проверки на реальном стримере перед публикацией релиза. В CI
это невозможно: LTO-привода и кассет на раннере нет, а filetape
покрывает формат и инварианты, но не поведение драйвера st, носителя
и ленточной механики. Поэтому: CI (unit/integration/e2e) + локальные
гейты + этот ручной чеклист.

База: [docs/func/ru/os-setup.md](func/ru/os-setup.md) — развёртывание
(rootless, systemd-юнит с харднингом); [docs/FORMAT.md](FORMAT.md) §11 —
DR-рецепт. Подразумевается стенд с реальным приводом (например, IBM
ULT3580-HH5, `/dev/nst0`); конфигурация стенда и живой журнал прогонов —
локальные заметки `логи/стенд-arch-skld00.md` (в git не попадают).

## Заметки совместимости

- Обновление с 1.0.0: миграции каталога автоматические
  (`PRAGMA user_version` при открытии БД); новые ключи TOML
  (`span_depth`, `verify`, webhook) выключены по умолчанию —
  существующие конфиги и ленты читаются без изменений.

## 0. Автоматизация перед ручными проверками

- [ ] CI на коммите-кандидате зелёный (workflow «Build and Test
      Lentovodets», push): web-build → build (CGO_ENABLED=0,
      статический) → `go vet` → gofmt check → `go test ./...`
      (±`-race` по входу `run_race_tests`; unit + integration
      вместе) → coverage (информационный) → Playwright E2E (opt-in
      `run_e2e_tests`). Шага golangci-lint в CI нет — линтер
      локальный гейт (следующий пункт).
- [ ] Локально: `make lint`, `make vet`, `make test`, `make
      test-race`, `make cover-check` (пороги — docs/TESTING.md §4)
      — зелёные.
- [ ] Локально: `go build -tags tape ./...`;
      `GOOS=linux go vet -tags tape ./...` (Windows-гейты tag-файлы
      не видят); кросс-сборка `GOOS=linux` amd64/arm64 с тегом
      `tape`.
- [ ] Интеграционные устойчивы: `go test ./test/integration/...
      -count=5`.
- [ ] `make web-build` + `make build` — зелёные (UI-бандл
      встраивается в бинарь).

## 1. Hardware-смоук на реальном стримере

Команды прогонов — в шапках файлов `test/hardware/` (env
`LENTOVODEC_TAPE_DEVICE`, по умолчанию `/dev/nst0`;
`LENTOVODEC_TAPE_REFORMAT=1` для уже размеченной кассеты).
Stdin-промпты смены кассет требуют запуска тестового бинаря
напрямую: `go test -c -tags=tape -o hw.test ./test/hardware/`.

- [ ] Базовый сценарий: `TestHardware_FormatBackupRestoreFull` —
      format → backup → readtest → restore full, дерево совпадает
      побайтово.
- [ ] Spanning на двух кассетах: `TestHardware_SpanningTwoTapes`
      (`LENTOVODEC_TAPE_SPAN_CAPACITY` меньше реальной ёмкости) —
      деление на части, смена по промпту, readtest и restore по
      цепочке, побайтовое сравнение.
- [ ] TapeAlert: `TestTapeAlerts` — LOG SENSE требует
      `CAP_SYS_RAWIO` (sudo env); без права — деградация в
      «диагностика недоступна», это норма, не блокер.
- [ ] Eject отрабатывает (кассета выбрасывается — норма).

## 2. DR-рецепт без Лентоводца

Автоматический эквивалент (`BareTarDRContract`) зелёный в CI;
ручная проверка оператором (FORMAT §11):

- [ ] `mt -f /dev/nst0 rewind && mt -f /dev/nst0 fsf 2 && dd
      if=/dev/nst0 bs=256k | tar -x` — файлы части кассеты
      извлекаются голым GNU tar, содержимое совпадает.

## 3. Негативные проверки

- [ ] Чужая/грязная кассета (первый блок короче 256 КиБ: legacy
      записи, LTFS): `tape format` отказывается честной ошибкой;
      после подготовки `mt-st weof` (os-setup.md) форматирование
      проходит.
- [ ] Не та кассета цепочки при restore: типизированная
      `ChainMismatchError` до восстановления данных.
- [ ] Повреждённая копия (порча байта tar-payload): smart-restore
      делает fallback на более старую копию; ни одной здоровой —
      честная `NoHealthyCopyError`.

## 4. Публикация

- [ ] Прокрутить [CHANGELOG.md](../CHANGELOG.md) и
      [CHANGELOG.EN.md](../CHANGELOG.EN.md): секция [Unreleased] →
      `[X.Y.Z] — дата релиза`, сверху свежий пустой [Unreleased];
      обновить compare-ссылки внизу обоих файлов; тело секции
      релиза — основа release notes.
- [ ] Тег `vX.Y.Z` на коммите после всех правок (версия бинаря —
      точный тег `v*` на HEAD → input `version_tag` → `sha-<hex8>`).
- [ ] Workflow «Build and Test Lentovodets» с
      `push_to_registry=true`, `publish_github=true`,
      `publish_codeberg=true` (секреты `UPLOAD_TOKEN`/
      `TOKEN_GITHUB`/`TOKEN_CODEBERG` настроены) — зелёный;
      артефакт `lentovodets-vX.Y.Z-linux-amd64` (+`.sha256`, с
      драйвером стримера, `-tags tape`) опубликован в Forgejo
      Packages + Release; релизы GitHub/Codeberg созданы.
- [ ] Release notes: тело секции [X.Y.Z] из CHANGELOG + ссылка на
      [docs/func/ru/](func/ru/).
