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

package sloglog_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"lentovodec/internal/adapter/sloglog"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{" warn ", slog.LevelWarn},
		{"error", slog.LevelError},
		{"мусор", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := sloglog.ParseLevel(tc.in); got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNew_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := sloglog.New("warn", &buf)

	log.Info("не должен попасть")
	log.Warn("должен попасть", "key", "value")
	out := buf.String()
	if strings.Contains(out, "не должен попасть") {
		t.Errorf("info прошёл при уровне warn:\n%s", out)
	}
	if !strings.Contains(out, "должен попасть") || !strings.Contains(out, "key=value") {
		t.Errorf("warn-запись повреждена:\n%s", out)
	}
}
