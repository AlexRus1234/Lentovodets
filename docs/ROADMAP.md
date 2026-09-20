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

# Дорожная карта (путеводитель)

Путеводитель: текущий статус, известные ограничения и направления
работ. Завершённые этапы — в [HISTORY.md](HISTORY.md); релизные
заметки уровня «что изменилось» — в корневом
[CHANGELOG.md](../CHANGELOG.md) (перевод —
[CHANGELOG.EN.md](../CHANGELOG.EN.md)); ручные проверки перед
публикацией — [RELEASE.md](RELEASE.md).

## Текущий статус

Код и документация выпущены по **v1.0.1** (2026-08-27): этапы 0–11
(скелет → домен/порты → адаптеры → use case → CLI/Web → CI →
spanning; факты — [HISTORY.md](HISTORY.md)) плюс волна сессий 9–20 по
итогам первого реального развёртывания (стенд IBM ULT3580-HH5,
LTO-4/5, Arch/SKLD00, 2026-08-22/25): резка по каталогам, файловый
браузер, спец-файлы, реконструкция каталога, verify-after-write,
версии файла в UI, TapeAlert, webhook, smart-restore/dest, дерево
файлов UI, скорость UI, доки кассеты/troubleshooting. Волна закрыта;
актуальных сессий нет.

Hardware-смоук на реальном стримере (format → backup → readtest →
restore full; spanning на двух кассетах) — зелёный (2026-08-22);
DR-рецепт голым mt/dd/tar закреплён автоматическим тестом
`BareTarDRContract`. Следующий релиз — по чеклисту
[RELEASE.md](RELEASE.md) при накоплении записей в **[Unreleased]**
[CHANGELOG.md](../CHANGELOG.md).

## Известные ограничения (решения планирования, не баги)

- Сессии демона не переживают рестарт (in-memory, SPEC §9.2) — отзыв
  всех сессий перезапуском; `api_key` бессрочный.
- `web_password_hash`/`api_key` хранятся в TOML в чистом/хешированном
  виде без секрета-менеджера — компромисс одномашинной модели
  (SPEC §9.2).
- Прогресс — поллинг раз в секунду; WebSocket/SSE — не-цель
  (SPEC §9.2).
- TapeAlert-диагностика требует `CAP_SYS_RAWIO` (ядро фильтрует
  LOG SENSE на st-нодах); без него деградирует в «диагностика
  недоступна» — core-функции (MTIOCTOP/read/write) не зависят.
- `catalog rebuild` — только local-CLI; REST/UI-кнопка и авто-смена
  кассет — бэклог (ниже).

## Направления пост-v1

Бэклог-кандидаты по итогам первого развёртывания (триаж владельца;
не сессии — перед стартом оформить в `логи/`, карта — в
`логи/README.md`, локально):

- причины «копия не читается» — в лог задачи UI (см. [CHANGELOG.md](../CHANGELOG.md),
  [1.0.1]: ошибка уже честная, но деталей в UI нет);
- честный текст `NoHealthyCopyError`: нет копий на кассете vs все
  не читаются;
- tape info с TapeAlert при нечитаемом ярлыке;
- `tape readtest` как действие в Web UI;
- предупреждение при filetape-устройстве в прод-конфиге;
- блок о выборе поколений LTO-кассет в features.md;
- `catalog rebuild`: REST/UI-кнопка и авто-смена кассет.

## Отклонённые идеи (не-цели)

- **Шифрование ленты** — конфликт с DR-рецептом голого tar и key
  management; отдельный разговор при изменении граничных условий.
- **Планировщик бекапов в демоне** — закрыто systemd timer +
  API-ключ.
- **Переключатель аппаратного сжатия LTO** — врёт `capacity`
  планировщику (нужно связывать с оценкой ёмкости).
