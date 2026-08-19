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

// Чтение файлов и метаданных поверх os.Open / os.Stat.

package osfs

import (
	"fmt"
	"io"
	"os"
	"sort"

	"lentovodec/internal/port"
)

// Open открывает файл для последовательного чтения.
func (f *FS) Open(path string) (io.ReadCloser, error) {
	rc, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osfs: открытие %q: %w", path, err)
	}
	return rc, nil
}

// ReadDir перечисляет содержимое каталога без фильтрации скрытых файлов.
// os.ReadDir использует lstat для DirEntry, поэтому symlink на каталог не
// превращается в каталог для навигации.
func (f *FS) ReadDir(path string) ([]port.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("osfs: чтение каталога %q: %w", path, err)
	}
	out := make([]port.DirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("osfs: сведения о %q: %w", entry.Name(), err)
		}
		out = append(out, port.DirEntry{
			Name: entry.Name(), IsDir: entry.IsDir(), Size: info.Size(), ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Stat возвращает сведения об элементе по пути.
func (f *FS) Stat(path string) (port.Entry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("osfs: stat %q: %w", path, err)
	}
	return entry{FileInfo: info}, nil
}

func (f *FS) Readlink(path string) (string, error) {
	link, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("osfs: readlink %q: %w", path, err)
	}
	return link, nil
}
