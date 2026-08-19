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

// Создание и удаление файлов и каталогов (для restore).

package osfs

import (
	"fmt"
	"io"
	"os"
)

// MkdirAll создаёт каталог и всех отсутствующих родителей.
func (f *FS) MkdirAll(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("osfs: mkdir %q: %w", path, err)
	}
	return nil
}

// Create создаёт файл (или усекает существующий) для записи.
func (f *FS) Create(path string) (io.WriteCloser, error) {
	wc, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("osfs: создание %q: %w", path, err)
	}
	return wc, nil
}

// Remove удаляет файл или пустой каталог.
func (f *FS) Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("osfs: удаление %q: %w", path, err)
	}
	return nil
}

func (f *FS) Symlink(linkname, path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("osfs: удаление перед symlink %q: %w", path, err)
	}
	if err := os.Symlink(linkname, path); err != nil {
		return fmt.Errorf("osfs: symlink %q: %w", path, err)
	}
	return nil
}

func (f *FS) Link(oldname, newname string) error {
	if err := os.Remove(newname); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("osfs: удаление перед link %q: %w", newname, err)
	}
	if err := os.Link(oldname, newname); err != nil {
		return fmt.Errorf("osfs: link %q: %w", newname, err)
	}
	return nil
}
