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

// Порт случайности. См. docs/ARCHITECTURE.md §6.2 (детерминированность).

package port

// Rand — источник случайности; заменяемая альтернатива rand.*
// внутри domain/usecase.
type Rand interface {
	// UUID4 возвращает UUID версии 4 (RFC-4122) в каноническом
	// виде: 8-4-4-4-12, lowercase.
	UUID4() (string, error)
}
