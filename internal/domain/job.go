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
