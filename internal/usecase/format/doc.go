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

// Package format реализует use case форматирования кассеты.
//
// Шаги (docs/ARCHITECTURE.md §4.3):
//
//  1. Tape.Rewind;
//  2. при !force — чтение существующего label, ErrAlreadyFormatted если есть;
//  3. label = {Magic, FormatVersion, Name, UUID=Rand.UUID4(),
//     FormattedAt=Clock.Now()};
//  4. WriteBlock(EncodeLabel(label));
//  5. WriteEOF;
//  6. WriteEOF (второй EOF = пустая лента с EOD сразу после ярлыка);
//  7. Catalog.RegisterTape(uuid, name).
//
// Двойной EOF после ярлыка — канонический EOD (docs/FORMAT.md §4).
package format
