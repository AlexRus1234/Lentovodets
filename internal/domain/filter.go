// Glob-фильтры исключений и нормализация путей.
// См. docs/SPECIFICATION.md §2.3.

package domain

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// NormalizePath приводит путь к каноническому виду: filepath.Clean и
// разделители '/'. Пустая строка нормализуется в ".".
func NormalizePath(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}

// MatchExclude сообщает, исключается ли путь хотя бы одним шаблоном.
//
// Перед сравнением путь и шаблон нормализуются NormalizePath; матчинг
// регистрозависительный, по полному пути. Семантика шаблонов:
//
//   - без "/" — шаблон матчит базовое имя: "*.tmp" исключит docs/x.tmp,
//     "node_modules" — любой каталог с таким именем;
//   - с "/" без "**" — glob по полному пути: "docs/*.md";
//   - с "**" — doublestar по полному пути: "**/.DS_Store" (матчит и
//     нулевую глубину), ".git/**" (матчит и сам каталог .git),
//     "/home/*/.cache/**".
//
// Используется path.Match, а не filepath.Match: у последнего на windows
// "*" пересекает '/', что делало бы семантику платформозависимой.
//
// Некорректный шаблон (например "[") пропускается; ошибка шаблона
// считается отсутствием матча.
func MatchExclude(p string, patterns []string) bool {
	normalized := NormalizePath(p)
	for _, pattern := range patterns {
		pattern = NormalizePath(pattern)
		switch {
		case strings.Contains(pattern, "**"):
			if matched, err := doublestar.Match(pattern, normalized); err == nil && matched {
				return true
			}
		case strings.Contains(pattern, "/"):
			if matched, err := path.Match(pattern, normalized); err == nil && matched {
				return true
			}
		default:
			if matched, err := path.Match(pattern, baseName(normalized)); err == nil && matched {
				return true
			}
		}
	}
	return false
}

// baseName — последний сегмент слэш-разделённого пути.
func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
