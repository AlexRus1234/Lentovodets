// Чтение файлов и метаданных поверх os.Open / os.Stat.

package osfs

import (
	"fmt"
	"io"
	"os"

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

// Stat возвращает сведения об элементе по пути.
func (f *FS) Stat(path string) (port.Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("osfs: stat %q: %w", path, err)
	}
	return info, nil
}
