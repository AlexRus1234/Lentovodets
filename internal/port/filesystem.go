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

// Порты файловой системы: обход, чтение, запись.
// См. docs/ARCHITECTURE.md §3, docs/ROADMAP.md Этап 2.
//
// os.FileMode и time.Time в сигнатурах — это типы-значения, а не I/O;
// depguard их в port разрешает.

package port

import (
	"context"
	"io"
	"os"
	"time"
)

// Entry — сведения об элементе ФС; подмножество os.FileInfo,
// достаточное для FileMeta и tar-заголовков.
type Entry interface {
	Name() string       // базовое имя элемента
	Size() int64        // размер в байтах; 0 для каталогов
	ModTime() time.Time // время последней модификации
	IsDir() bool
	Mode() os.FileMode
}

// Walker — рекурсивный обход дерева от root. Для каждого элемента
// (включая сам root) вызывается fn; порядок обхода не гарантирован.
// Ошибка из fn останавливает обход и возвращается из Walk.
type Walker interface {
	Walk(ctx context.Context, root string, fn func(path string, info Entry) error) error
}

// FileReader — чтение файлов и получение метаданных.
type FileReader interface {
	// Open открывает файл по пути для последовательного чтения.
	Open(path string) (io.ReadCloser, error)

	// Stat возвращает сведения об элементе по пути.
	Stat(path string) (Entry, error)
}

// FileWriter — создание и удаление файлов и каталогов (для restore).
type FileWriter interface {
	// MkdirAll создаёт каталог и всех отсутствующих родителей.
	MkdirAll(path string, perm os.FileMode) error

	// Create создаёт файл (или усекает существующий) для записи.
	Create(path string) (io.WriteCloser, error)

	// Remove удаляет файл или пустой каталог.
	Remove(path string) error
}

// Filesystem — полный FS-порт одной зависимостью: реализация
// Walker + FileReader + FileWriter (adapter/osfs, testutil MapFS).
type Filesystem interface {
	Walker
	FileReader
	FileWriter
}
