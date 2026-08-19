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

// MapFS — двойник port.Filesystem поверх testing/fstest.MapFS.
// См. docs/TESTING.md §3.2.

package testutil

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"lentovodec/internal/port"
)

// MapFS реализует port.Filesystem поверх fstest.MapFS. Пути в конструкторе
// и методах — как в FileMeta.Path (Clean, ведущий '/' необязателен);
// внутри хранятся относительными (требование fs.FS).
//
// Файлы по умолчанию имеют режим 0644, каталоги — 0755; mtime — нулевой
// момент (тесты, которым нужны конкретные mtime, меняют поля fstest.MapFile
// напрямую: fs.MapFS["etc/hosts"].ModTime = ...).
type MapFS struct {
	fstest.MapFS
	mu sync.Mutex
}

// NewMapFS создаёт файловую систему из карты путь→содержимое.
func NewMapFS(files map[string]string) *MapFS {
	m := &MapFS{MapFS: fstest.MapFS{}}
	for p, content := range files {
		m.MapFS[toFSName(p)] = &fstest.MapFile{Data: []byte(content), Mode: 0o644}
	}
	return m
}

// Walk обходит дерево от root, передавая абсолютные Clean-пути.
func (m *MapFS) Walk(ctx context.Context, root string, fn func(p string, info port.Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	base := toFSName(root)
	return fs.WalkDir(m.MapFS, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(fromFSName(p), info)
	})
}

// Open открывает файл для чтения.
func (m *MapFS) Open(p string) (io.ReadCloser, error) {
	f, err := m.MapFS.Open(toFSName(p))
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Stat возвращает сведения об элементе.
func (m *MapFS) Stat(p string) (port.Entry, error) {
	info, err := fs.Stat(m.MapFS, toFSName(p))
	if err != nil {
		return nil, err
	}
	return info, nil
}

// ReadDir перечисляет непосредственное содержимое каталога в том же порядке,
// что и osfs. Ссылки в MapFS также не разыменовываются.
func (m *MapFS) ReadDir(p string) ([]port.DirEntry, error) {
	name := toFSName(p)
	if name == "" {
		name = "."
	}
	entries, err := fs.ReadDir(m.MapFS, name)
	if err != nil {
		return nil, err
	}
	out := make([]port.DirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
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

// MkdirAll создаёт каталог и всех отсутствующих родителей.
func (m *MapFS) MkdirAll(p string, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	segs := strings.Split(toFSName(p), "/")
	for i := 1; i <= len(segs); i++ {
		name := strings.Join(segs[:i], "/")
		if _, ok := m.MapFS[name]; ok {
			continue
		}
		m.MapFS[name] = &fstest.MapFile{Mode: fs.ModeDir | perm, ModTime: time.Unix(0, 0)}
	}
	return nil
}

// Create создаёт (или усекает) файл для записи; Close фиксирует содержимое.
func (m *MapFS) Create(p string) (io.WriteCloser, error) {
	return &mapWriter{m: m, name: toFSName(p)}, nil
}

// Remove удаляет файл или каталог.
func (m *MapFS) Remove(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := toFSName(p)
	if _, ok := m.MapFS[name]; !ok {
		return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
	}
	delete(m.MapFS, name)
	return nil
}

// mapWriter накапливает записанное и складывает в карту на Close.
type mapWriter struct {
	m      *MapFS
	name   string
	buf    []byte
	closed bool
}

// Write реализует io.Writer.
func (w *mapWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("mapfs: запись в закрытый файл %q", w.name)
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// Close фиксирует содержимое файла в карте.
func (w *mapWriter) Close() error {
	if w.closed {
		return fmt.Errorf("mapfs: повторный Close файла %q", w.name)
	}
	w.closed = true
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	prev := w.m.MapFS[w.name]
	mode := fs.FileMode(0o644)
	var modTime time.Time
	if prev != nil {
		mode = prev.Mode
		modTime = prev.ModTime
	}
	w.m.MapFS[w.name] = &fstest.MapFile{Data: w.buf, Mode: mode, ModTime: modTime}
	return nil
}

// toFSName приводит путь к виду fs.FS: относительный, Clean, слэши.
func toFSName(p string) string {
	cleaned := path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	return strings.TrimPrefix(cleaned, "/")
}

// fromFSName возвращает абсолютный Clean-путь (как в FileMeta.Path).
func fromFSName(name string) string {
	return "/" + name
}
