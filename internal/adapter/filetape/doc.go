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

// Package filetape реализует port.Tape поверх обычного файла.
//
// Назначение — dev/CI: эмулировать стример одним файлом. Хранит байты +
// slice позиций filemark'ов; семантика ForwardFilemarks/EndOfData повторяет
// MTFSF/MTEOM (docs/TESTING.md §3.1).
//
// Один файл — одна лента; для тестов создаётся в t.TempDir(). Это
// реализация "ленты как файла", а не тестовый двойник:FakeTape из testutil.
package filetape
