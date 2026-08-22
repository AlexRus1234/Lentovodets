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

// Package tapeformat реализует чистый формат ленты (без ввода-вывода).
//
// Здесь живут:
//
//   - encoder: WriteSession(tape, files, fs, prog) — JSON-индекс (добитый
//     до BlockSize), WriteEOF, tar-поток, WriteEOF;
//   - decoder: ReadSession(tape, dest, include, prog) ([]FileMeta, error) — чтение
//     индекса, затем tar с обязательной проверкой xxhash;
//   - label: EncodeLabel(label) []byte / DecodeLabel(block) (TapeLabel, error).
//
// Канон формата — docs/FORMAT.md. Блочный I/O делегируется в port.Tape,
// поэтому пакет тестируется на FakeTape с круглым round-trip и golden-файлами.
// Целевое покрытие: 100%.
package tapeformat
