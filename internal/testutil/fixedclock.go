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

// FixedClock — двойник port.Clock: всегда одно и то же время.
// См. docs/TESTING.md §3.5.

package testutil

import (
	"time"

	"lentovodec/internal/port"
)

// fixedClock — реализация port.Clock на зафиксированном моменте.
type fixedClock struct{ t time.Time }

// FixedClock создаёт часы, замороженные на t.
func FixedClock(t time.Time) port.Clock {
	return fixedClock{t: t}
}

// Now возвращает зафиксированное время.
func (c fixedClock) Now() time.Time { return c.t }
