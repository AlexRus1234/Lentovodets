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

// Дайджест сверки содержимого файла: sha256 у записей с полем SHA256
// (новые ленты, ишью №4), xxhash64 у записанных до поля. Ровно один
// активный дайджест — вторая сумма удваивает работу чтения, не добавляя
// проверки (docs/FORMAT.md §7).

package tapeformat

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"

	"github.com/cespare/xxhash/v2"

	"lentovodec/internal/domain"
)

// contentDigest выбирается наличием поля SHA256 записи индекса.
type contentDigest struct {
	h    hash.Hash // sha256, если поле задано
	xx   *xxhash.Digest
	want string // ожидаемая hex-сумма (SHA256 либо Hash)
}

// newContentDigest строит дайджест сверки для записи индекса.
func newContentDigest(fm *domain.FileMeta) *contentDigest {
	if fm.SHA256 != "" {
		return &contentDigest{h: sha256.New(), want: fm.SHA256}
	}
	return &contentDigest{xx: xxhash.New(), want: fm.Hash}
}

// Write — io.Writer для MultiWriter рядом с tar-потоком.
func (d *contentDigest) Write(p []byte) (int, error) {
	if d.h != nil {
		return d.h.Write(p)
	}
	return d.xx.Write(p)
}

// result возвращает фактическую и ожидаемую hex-суммы.
func (d *contentDigest) result() (got, want string) {
	if d.h != nil {
		return hex.EncodeToString(d.h.Sum(nil)), d.want
	}
	return hashHex(d.xx.Sum64()), d.want
}
