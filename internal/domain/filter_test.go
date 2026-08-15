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
