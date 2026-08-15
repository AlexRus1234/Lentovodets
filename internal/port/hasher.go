// Порт хеширования файлов. См. docs/FORMAT.md §2 (xxhash64, hex 16).

package port

import "io"

// Hasher — вычисление контрольной суммы файла.
// Реализация: adapter/xxhash.
type Hasher interface {
	// Hash возвращает канонический hex-xxhash64 (16 символов,
	// lowercase) содержимого r.
	Hash(r io.Reader) (string, error)
}
