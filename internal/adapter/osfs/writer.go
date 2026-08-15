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
