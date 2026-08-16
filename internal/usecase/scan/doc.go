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

// Package scan реализует use case сканирования файловой системы.
//
// Scanner сравнивает текущий снимок ФС (через port.Filesystem) с прошлым
// снимком из port.Catalog и для каждого пути вычисляет FileState:
//
//   - Added    — файла не было в прошлой сессии;
//   - Modified — изменился Size или ModTime (или Hash при full check);
//   - Deleted  — был в прошлой сессии, теперь отсутствует
//     (только для job.Mode == mirror; становится tombstone).
//
// Целевое покрытие: >= 95% (docs/TESTING.md).
package scan
