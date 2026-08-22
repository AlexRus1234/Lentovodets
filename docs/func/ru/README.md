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

# Документация Лентоводца

Каталог содержит функциональную документацию Лентоводца. Корневой
`README.md` предоставляет обзор системы и инструкцию по первоначальному
запуску; сведения, требующие детального описания, приведены в настоящем
каталоге.

| Файл | О чём |
|---|---|
| [features.md](features.md) | Возможности: задания и режимы бекапа, восстановление, каталог, лента, демон, Web UI и безопасность. |
| [cli.md](cli.md) | Справочник CLI: глобальные флаги, команды local/daemon, выбор устройства, примеры. |
| [api.md](api.md) | REST API демона: аутентификация, эндпоинты, фоновые задачи и прогресс. |
| [os-setup.md](os-setup.md) | Развёртывание в Linux: rootless-модель, группа `tape`, udev, systemd-юнит, сетевой доступ. |
| [tape-format.md](tape-format.md) | Формат ленты глазами оператора: ярлык, сессии, filemark'и, совместимость. |

Английская документация — в каталоге [`docs/func/EN/`](../EN/README.md).

Внутренняя документация разработки — в каталоге [`docs/`](../../):
`ARCHITECTURE.md` (слои и правила импортов), `SPECIFICATION.md`
(полные требования и схемы), `FORMAT.md` (байтовый канон формата),
`TESTING.md` (стратегия тестирования), `ROADMAP.md` (история этапов).
