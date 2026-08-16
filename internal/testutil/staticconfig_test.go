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

	"lentovodec/internal/domain"
)

func TestStaticConfig_JobsRoundTrip(t *testing.T) {
	jobs := []domain.Job{{
		Name:  "daily",
		Mode:  domain.ModeMirror,
		Paths: []string{"/tank/data"},
	}}
	c := &StaticConfig{JobList: jobs}

	got, err := c.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(got) != 1 || got[0].Name != "daily" {
		t.Fatalf("Jobs: got %v, want исходный список", got)
	}
	if c.Device() != "" || c.DB() != "" || c.Log() != "" || c.Server() != "" {
		t.Fatal("геттеры путей должны возвращать пустые строки")
	}
	if c.LogLevel() != "info" {
		t.Fatalf("LogLevel: got %q, want info", c.LogLevel())
	}
}
