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

// Package restore реализует use case восстановления с ленты.
//
// Три варианта (см. docs/SPECIFICATION.md §4.3), все проверяют xxhash:
//
//   - RestoreFull      — перемотка в начало, чтение всех сессий;
//   - RestoreSelective — MTFSF(2*K-1) к сессии K, выбор путей;
//   - RestoreSmart     — Catalog.GetAllFileCopies(path), перебор копий
//     до первой здоровой; иначе ErrNoHealthyCopy.
package restore
