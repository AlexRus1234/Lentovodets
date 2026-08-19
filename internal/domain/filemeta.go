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

// Метаданные файла в сессии. См. docs/SPECIFICATION.md §2.4–2.5
// и docs/FORMAT.md §6.

package domain

import "fmt"

// FileType describes how a filesystem entry is represented in the archive.
type FileType string

const (
	FileTypeRegular  FileType = "reg"
	FileTypeSymlink  FileType = "sym"
	FileTypeHardlink FileType = "lnk"
	// Short names are kept next to the wire values for callers constructing metadata.
	TypeReg      = FileTypeRegular
	TypeSym      = FileTypeSymlink
	TypeLink     = FileTypeHardlink
	FileTypeReg  = FileTypeRegular
	FileTypeSym  = FileTypeSymlink
	FileTypeLink = FileTypeHardlink
)

func (t FileType) Valid() bool {
	return t == "" || t == FileTypeRegular || t == FileTypeSymlink || t == FileTypeHardlink
}

func (t FileType) normalized() FileType {
	if t == "" {
		return FileTypeRegular
	}
	return t
}

// FileState — состояние файла относительно предыдущей сессии.
// Значение совпадает с символом в БД и JSON-индексе на ленте.
type FileState string

// Допустимые значения FileState.
const (
	// StateAdded — файл есть на ФС, в прошлой сессии не встречался ('A').
	StateAdded FileState = "A"

	// StateModified — файл есть, изменился Size или ModTime ('M').
	StateModified FileState = "M"

	// StateDeleted — tombstone: файл был в прошлой сессии, теперь
	// отсутствует; только режим mirror ('D'). В tar-поток не попадает.
	StateDeleted FileState = "D"
)

// Valid сообщает, что значение — один из допустимых символов состояния.
func (s FileState) Valid() bool {
	return s == StateAdded || s == StateModified || s == StateDeleted
}

// FileMeta — метаданные одного файла в конкретной сессии.
type FileMeta struct {
	Path     string    `json:"path"`     // всегда NormalizePath
	Size     int64     `json:"size"`     // байты; 0 для каталогов
	ModTime  int64     `json:"mod_time"` // Unix-наносекунды mtime файла
	IsDir    bool      `json:"is_dir"`   // true для каталогов
	Hash     string    `json:"hash"`     // xxhash64 hex (16 символов); "" для каталогов и tombstone'ов
	State    FileState `json:"state"`
	Type     FileType  `json:"type,omitempty"`
	Linkname string    `json:"linkname,omitempty"`
}

// IsAdded сообщает, что файл новый в этой сессии.
func (fm FileMeta) IsAdded() bool { return fm.State == StateAdded }

// IsDeleted сообщает, что запись — tombstone на удалённый файл.
func (fm FileMeta) IsDeleted() bool { return fm.State == StateDeleted }

func (fm FileMeta) IsSymlink() bool  { return fm.Type.normalized() == FileTypeSymlink }
func (fm FileMeta) IsHardlink() bool { return fm.Type.normalized() == FileTypeHardlink }

// Validate checks the additive special-file invariants. Empty Type is the
// representation used by old indexes and means a regular file.
func (fm FileMeta) Validate() error {
	if !fm.ValidType() {
		return fmt.Errorf("file %q: invalid type %q", fm.Path, fm.Type)
	}
	if (fm.IsSymlink() || fm.IsHardlink()) && fm.Linkname == "" {
		return fmt.Errorf("file %q: %s has no linkname", fm.Path, fm.Type)
	}
	if !fm.IsSymlink() && !fm.IsHardlink() && fm.Linkname != "" {
		return fmt.Errorf("file %q: regular file has linkname", fm.Path)
	}
	return nil
}

func (fm FileMeta) ValidType() bool { return fm.Type.Valid() }
