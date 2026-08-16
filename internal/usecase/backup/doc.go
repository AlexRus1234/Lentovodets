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

// Package backup реализует use case резервного копирования на ленту.
//
// Алгоритм — в docs/ARCHITECTURE.md §4.1:
// загрузка Job → чтение TapeLabel → определение sessionNum и Full/Inc →
// Scanner.Scan → Catalog.CreateSession → Tape.Rewind/LocateEOD →
// tapeformat.WriteSession → Catalog.SaveFiles.
//
// Опции: Full (полный бекап), DryRun (только сканирование).
// Возвращает Session и Stats{Scanned, Added, Modified, Deleted, Bytes}.
package backup
