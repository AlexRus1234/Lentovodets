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

// Внутренний тест: приватные чистые функции корня команды.

package cli

import (
	"testing"
)

// TestHumanSize — компактный формат размеров.
func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{5 * 1024 * 1024 * 1024, "5.0 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1.0 PiB"},
	}
	for _, tc := range cases {
		if got := humanSize(tc.n); got != tc.want {
			t.Errorf("humanSize(%d) = %q; want %q", tc.n, got, tc.want)
		}
	}
}

// TestJoinBind — флаги поверх адреса конфига по отдельности.
func TestJoinBind(t *testing.T) {
	cases := []struct {
		name       string
		bind, port string
		config     string
		intPort    int
		want       string
	}{
		{"всё из конфига", "", "", "192.168.1.5:29201", 0, "192.168.1.5:29201"},
		{"только порт", "", "", "192.168.1.5:29201", 29300, "192.168.1.5:29300"},
		{"только хост", "0.0.0.0", "", "127.0.0.1:29201", 0, "0.0.0.0:29201"},
		{"оба флага", "10.0.0.1", "", "localhost:1", 29400, "10.0.0.1:29400"},
		{"битый конфиг — дефолт", "", "", "not-a-hostport", 0, "127.0.0.1:29201"},
		{"пустой хост из флага", "", "", "127.0.0.1:29201", 29500, "127.0.0.1:29500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinBind(tc.bind, tc.intPort, tc.config); got != tc.want {
				t.Fatalf("joinBind(%q, %d, %q) = %q; want %q",
					tc.bind, tc.intPort, tc.config, got, tc.want)
			}
		})
	}
}
