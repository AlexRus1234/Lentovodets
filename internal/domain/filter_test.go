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

package domain_test

import (
	"runtime"
	"testing"

	"lentovodec/internal/domain"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "."},
		{".", "."},
		{"./x", "x"},
		{"a/b/../c", "a/c"},
		{"a//b", "a/b"},
		{"a/b/", "a/b"},
		{"/", "/"},
		{"/tank/./data/../data/media", "/tank/data/media"},
	}
	for _, tt := range tests {
		if got := domain.NormalizePath(tt.in); got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestNormalizePath_AbsoluteAndRelativeDistinct — регрессия сессии 18:
// Web UI строит дерево, срезая ведущий '/' каталоговых путей, но в API
// (restore/start, file-copies) путь должен уходить ровно тот, что в
// каталоге. Относительное «tank/…» и абсолютное «/tank/…» обязаны
// оставаться разными строками: копии ищутся по точному пути, потеря
// слэша превращается в «все копии не читаются».
func TestNormalizePath_AbsoluteAndRelativeDistinct(t *testing.T) {
	rel := domain.NormalizePath("tank/data/medTEST/TT/video.mkv")
	abs := domain.NormalizePath("/tank/data/medTEST/TT/video.mkv")
	if rel == abs {
		t.Fatalf("NormalizePath склеила относительный и абсолютный путь: %q == %q", rel, abs)
	}
	if want := "tank/data/medTEST/TT/video.mkv"; rel != want {
		t.Errorf("NormalizePath(относительный) = %q, want %q", rel, want)
	}
	if want := "/tank/data/medTEST/TT/video.mkv"; abs != want {
		t.Errorf("NormalizePath(абсолютный) = %q, want %q", abs, want)
	}
}

func TestNormalizePathWindowsSeparators(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("разделители '\\' специфичны для windows")
	}
	tests := []struct{ in, want string }{
		{`a\b\..\c`, "a/c"},
		{`.\x`, "x"},
		{`C:\tank\data`, "C:/tank/data"},
	}
	for _, tt := range tests {
		if got := domain.NormalizePath(tt.in); got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchExclude(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		want     bool
	}{
		{"без шаблонов", "a/b.txt", nil, false},
		{"пустой шаблон", "a/b.txt", []string{""}, false},

		{"базовое имя: матч", "docs/report.tmp", []string{"*.tmp"}, true},
		{"базовое имя: мимо", "docs/report.txt", []string{"*.tmp"}, false},
		{"регистр значим", "docs/REPORT.TMP", []string{"*.tmp"}, false},
		{"точное имя каталога", "/srv/www/node_modules", []string{"node_modules"}, true},
		{"префикс имени не матчит", "/srv/www/node_modulesX", []string{"node_modules"}, false},

		{"полный путь: матч", "docs/readme.md", []string{"docs/*.md"}, true},
		{"полный путь: мимо", "src/readme.md", []string{"docs/*.md"}, false},
		{"звёздочка не пересекает сегмент", "docs/a/readme.md", []string{"docs/*.md"}, false},

		{"doublestar вглубь", ".git/hooks/pre-commit", []string{".git/**"}, true},
		{"doublestar сам корневой каталог", ".git", []string{".git/**"}, true},
		{"doublestar привязан к корню", "proj/.git/hooks/pre-commit", []string{".git/**"}, false},
		{"doublestar с ведущим **", "proj/.git/hooks/pre-commit", []string{"**/.git/**"}, true},
		{"doublestar нулевая глубина", ".DS_Store", []string{"**/.DS_Store"}, true},
		{"doublestar вложенно", "a/b/.DS_Store", []string{"**/.DS_Store"}, true},
		{"абсолютный шаблон: матч", "/home/u/.cache/browser", []string{"/home/*/.cache/**"}, true},
		{"абсолютный шаблон: мимо", "/var/tmp/x", []string{"/home/*/.cache/**"}, false},

		{"матч во втором шаблоне", "b.log", []string{"*.tmp", "*.log"}, true},
		{"битый шаблон пропускается", "anything", []string{"[", "any*"}, true},
		{"только битый шаблон", "anything", []string{"["}, false},
		{"битый doublestar пропускается", "x/y", []string{"**/["}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.MatchExclude(tt.path, tt.patterns); got != tt.want {
				t.Errorf("MatchExclude(%q, %q) = %v, want %v",
					tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestMatchExcludeWindowsSeparators(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("разделители '\\' специфичны для windows")
	}
	if !domain.MatchExclude(`docs\x.tmp`, []string{"*.tmp"}) {
		t.Error("MatchExclude должен нормализовать '\\' перед матчингом")
	}
	if !domain.MatchExclude(`docs\readme.md`, []string{"docs/*.md"}) {
		t.Error("шаблон полного пути должен матчить windows-путь после нормализации")
	}
	if domain.MatchExclude(`docs\x.txt`, []string{"docs/*.md"}) {
		t.Error("несовпадающий шаблон не должен матчить windows-путь")
	}
}
