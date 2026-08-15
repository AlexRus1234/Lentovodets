// Хеширование файлов: xxhash64 в hex (16 символов).

package xxhash

import (
	"fmt"
	"io"

	"github.com/cespare/xxhash/v2"
)

// Hasher реализует port.Hasher поверх cespare/xxhash/v2.
type Hasher struct{}

// New создаёт хешировщик.
func New() *Hasher {
	return &Hasher{}
}

// Hash возвращает hex-xxhash64 содержимого r.
func (h *Hasher) Hash(r io.Reader) (string, error) {
	digest := xxhash.New()
	buf := make([]byte, 64<<10)
	if _, err := io.CopyBuffer(digest, r, buf); err != nil {
		return "", fmt.Errorf("xxhash: чтение потока: %w", err)
	}
	return hashHex(digest.Sum64()), nil
}

// hashHex — каноническое представление xxhash64: 16 hex-символов,
// lowercase (docs/FORMAT.md §2); совпадает с tapeformat.
func hashHex(sum uint64) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = digits[sum&0xf]
		sum >>= 4
	}
	return string(out)
}
