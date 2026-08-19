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

// Package destfs — декоратор port.Filesystem для восстановления в
// отдельный каталог: пути записи (MkdirAll/Create/Remove) переносятся
// под корень dest, чтение и обход проходят без изменений. Нужен CLI
// `restore --dest` и REST `POST /api/restore/start?dest=`; сам use case
// restore про dest не знает (пишет по путям из индекса ленты).
package destfs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lentovodec/internal/port"
)

// Wrap оборачивает inner так, что файлы с путями из индекса ленты
// (абсолютные unix-пути или пути с именем тома) пишутся под dest:
// "/etc/hosts" → dest/etc/hosts. Пустой dest возвращает inner как есть.
func Wrap(inner port.Filesystem, dest string) port.Filesystem {
	if dest == "" {
		return inner
	}
	return &relocFS{inner: inner, dest: filepath.Clean(dest)}
}

// relocFS — port.Filesystem с переносом путей записи под dest.
type relocFS struct {
	inner port.Filesystem
	dest  string
}

// Walk передаётся внутренней ФС без изменений (в restore не участвует).
func (f *relocFS) Walk(ctx context.Context, root string, fn func(string, port.Entry) error) error {
	return f.inner.Walk(ctx, root, fn)
}

// Open передаётся внутренней ФС без изменений.
func (f *relocFS) Open(path string) (io.ReadCloser, error) {
	return f.inner.Open(path)
}

// Stat передаётся внутренней ФС без изменений.
func (f *relocFS) Stat(path string) (port.Entry, error) {
	return f.inner.Stat(path)
}

func (f *relocFS) Readlink(path string) (string, error) { return f.inner.Readlink(path) }

// ReadDir передаётся внутренней ФС без изменений: браузер показывает ФС
// сервера, а не виртуальное дерево каталога назначения.
func (f *relocFS) ReadDir(path string) ([]port.DirEntry, error) {
	return f.inner.ReadDir(path)
}

// MkdirAll создаёт каталог под dest.
func (f *relocFS) MkdirAll(path string, perm os.FileMode) error {
	return f.inner.MkdirAll(f.relocate(path), perm)
}

// Create создаёт файл под dest.
func (f *relocFS) Create(path string) (io.WriteCloser, error) {
	return f.inner.Create(f.relocate(path))
}

// Remove удаляет файл/каталог под dest (tombstone'ы mirror-restore).
func (f *relocFS) Remove(path string) error {
	return f.inner.Remove(f.relocate(path))
}

func (f *relocFS) Symlink(linkname, path string) error {
	return f.inner.Symlink(linkname, f.relocate(path))
}

func (f *relocFS) Link(oldname, newname string) error {
	return f.inner.Link(f.relocate(oldname), f.relocate(newname))
}

// relocate переносит путь из индекса под корень dest: отрезаются
// ведущий '/' и имя тома ("C:"), остальное присоединяется к dest.
func (f *relocFS) relocate(p string) string {
	clean := filepath.Clean(p)
	rest := strings.TrimPrefix(clean, filepath.VolumeName(clean))
	rest = filepath.ToSlash(rest)
	// Имя тома срезается и там, где текущая ОС его не видит: индекс
	// мог быть записан на другой ОС ("C:/data" при restore на Linux).
	if len(rest) > 2 && rest[0] != '/' && rest[1] == ':' && rest[2] == '/' {
		rest = rest[2:]
	}
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" || rest == "." {
		return f.dest
	}
	return filepath.Join(f.dest, filepath.FromSlash(rest))
}
