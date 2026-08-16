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

package testutil

import (
	"testing"
	"time"
)

func TestStepClock_AdvancesOneSecondPerCall(t *testing.T) {
	start := time.Unix(1700000000, 0)
	c := StepClock(start)

	for i, want := range []time.Time{
		start,
		start.Add(1 * time.Second),
		start.Add(2 * time.Second),
		start.Add(3 * time.Second),
	} {
		if got := c.Now(); !got.Equal(want) {
			t.Fatalf("вызов %d: got %v, want %v", i+1, got, want)
		}
	}
}
