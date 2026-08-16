// Лентоводец — система резервного копирования на ленточные накопители LTO
// Copyright (C) 2026 AlexRus1234
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Package cli — слой доставки на cobra + viper.
//
// Одна подкоманда — один файл (docs/SPECIFICATION.md §5). Команды
// daemon-режима (catalog/*, tape info/eject) ходят в HTTP API демона через
// внутренний HTTPClient; локальные команды (backup, restore, tape format,
// jobs *) работают напрямую с лентой без демона.
//
// Тонкая обёртка: вся логика — в usecase. Здесь только парсинг флагов,
// DI-композиция и вывод.
package cli
