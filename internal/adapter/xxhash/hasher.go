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
