// Конфигурация задания бекапа. См. docs/SPECIFICATION.md §2.1–2.2.

package domain

import (
	"errors"
	"fmt"
	"strings"
)

// JobMode — режим отслеживания изменений задания.
type JobMode string

// Допустимые значения JobMode.
const (
	// ModeAppend — бекапятся новые и изменённые файлы; удаления
	// не отслеживаются.
	ModeAppend JobMode = "append"

	// ModeMirror — как append, плюс tombstone'ы на удалённые файлы
	// (FileState Deleted): восстановление может реконструировать
	// зеркало каталога на момент любой сессии.
	ModeMirror JobMode = "mirror"
)

// Valid сообщает, что значение — один из допустимых режимов.
func (m JobMode) Valid() bool {
	return m == ModeAppend || m == ModeMirror
}

// Job — конфигурация одного задания бекапа; хранится в секции
// [[jobs]] файла lentovodec.toml.
type Job struct {
	Name        string   // уникальное имя задания; ключ в конфиге
	Description string   // человекочитаемое описание
	Mode        JobMode  // append или mirror
	Paths       []string // корни бекапа (файлы или каталоги)
	Exclude     []string // glob-шаблоны исключений; семантика — MatchExclude
}

// Validate проверяет целостность задания: непустое имя, допустимый
// режим, непустой список путей без пустых элементов.
func (j Job) Validate() error {
	if j.Name == "" {
		return errors.New("имя задания не может быть пустым")
	}
	if !j.Mode.Valid() {
		return fmt.Errorf("задание %q: недопустимый режим %q (ожидается %q или %q)",
			j.Name, j.Mode, ModeAppend, ModeMirror)
	}
	if len(j.Paths) == 0 {
		return fmt.Errorf("задание %q: список путей пуст", j.Name)
	}
	if hasEmptyPath(j.Paths) {
		return fmt.Errorf("задание %q: один из путей пуст", j.Name)
	}
	return nil
}

func hasEmptyPath(paths []string) bool {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return true
		}
	}
	return false
}
