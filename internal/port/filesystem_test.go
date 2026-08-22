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

package port_test

import (
	"os"
	"testing"

	"lentovodec/internal/port"
)

func TestIsSpecial(t *testing.T) {
	cases := []struct {
		mode os.FileMode
		want bool
	}{
		{0, false},
		{os.ModeDir, false},
		{os.ModeSymlink, false},
		{os.ModePerm, false},
		{os.ModeNamedPipe, true},
		{os.ModeSocket, true},
		{os.ModeDevice, true},
		{os.ModeDevice | os.ModeCharDevice, true},
		{os.ModeDir | os.ModeNamedPipe, true},
	}
	for _, tc := range cases {
		if got := port.IsSpecial(tc.mode); got != tc.want {
			t.Errorf("IsSpecial(%v) = %v; want %v", tc.mode, got, tc.want)
		}
	}
}

func TestIsSymlink(t *testing.T) {
	if !port.IsSymlink(os.ModeSymlink | 0o777) {
		t.Error("IsSymlink(symlink) = false")
	}
	if port.IsSymlink(os.ModeDir) {
		t.Error("IsSymlink(dir) = true")
	}
}
