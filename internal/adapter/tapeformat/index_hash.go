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

// Контрольная сумма Session Index (docs/FORMAT.md §5.2): sha256 над
// канонической формой индекса. Правило версионируется строкой hash_algo;
// сами поля index_sha256/hash_algo в форму не входят.

package tapeformat

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"

	"lentovodec/internal/domain"
)

// IndexHashAlgo — идентификатор правила канонической формы индекса.
const IndexHashAlgo = "sha256-canon1"

// canonicalPrefix — доменное разделение правила: хеш другого объекта с
// теми же полями не совпадёт с индексным.
const canonicalPrefix = "lentovodec.index.canon1\n"

// canonicalIndex возвращает каноническую форму индекса: поля в
// фиксированном порядке, каждое — длина (uint64 big-endian) + байты.
// Длина вместо разделителей снимает вопрос экранирования: имя файла на
// Linux может содержать любые байты, кроме NUL и '/'.
// Числа — десятичный ASCII, bool — "0"/"1", пустая строка — длина 0.
func canonicalIndex(idx *SessionIndex) []byte {
	var buf bytes.Buffer
	buf.WriteString(canonicalPrefix)
	writeField := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		buf.Write(n[:])
		buf.WriteString(s)
	}
	writeField(strconv.Itoa(idx.FormatVersion))
	writeField(strconv.FormatInt(int64(idx.SessionNum), 10))
	writeField(string(idx.Type))
	writeField(idx.JobRunID)
	writeField(strconv.FormatInt(idx.Timestamp, 10))
	writeField(idx.JobName)
	writeField(strconv.FormatInt(int64(idx.Part), 10))
	writeField(idx.Continues)
	writeField(strconv.Itoa(len(idx.Files)))
	for i := range idx.Files {
		fm := &idx.Files[i]
		writeField(fm.Path)
		writeField(strconv.FormatInt(fm.Size, 10))
		writeField(strconv.FormatInt(fm.ModTime, 10))
		writeField(boolField(fm.IsDir))
		writeField(fm.Hash)
		writeField(string(fm.State))
		writeField(string(fm.Type))
		writeField(fm.Linkname)
	}
	return buf.Bytes()
}

// boolField кодирует bool фиксированно, без зависимости от формата
// strconv.FormatBool.
func boolField(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// indexDigest — hex sha256 канонической формы индекса.
func indexDigest(idx *SessionIndex) string {
	sum := sha256.Sum256(canonicalIndex(idx))
	return hex.EncodeToString(sum[:])
}

// sealIndex проставляет правило и контрольную сумму перед записью.
func sealIndex(idx *SessionIndex) {
	idx.HashAlgo = IndexHashAlgo
	idx.IndexSHA256 = indexDigest(idx)
}

// verifyIndexHash проверяет контрольную сумму разобранного индекса до
// нормализации и использования полей. Отсутствие поля — лента записана до
// появления КС: проверка не выполняется. Неизвестное правило — индекс
// записан более новым бинарём: проверить нечем, чтение не блокируется
// (DR важнее обнаружения подмены; см. docs/FORMAT.md §5.2).
func verifyIndexHash(idx *SessionIndex) error {
	if idx.IndexSHA256 == "" || idx.HashAlgo != IndexHashAlgo {
		return nil
	}
	if got := indexDigest(idx); got != idx.IndexSHA256 {
		return &domain.IndexCorruptError{
			SessionNum: idx.SessionNum,
			Algo:       idx.HashAlgo,
			Recorded:   idx.IndexSHA256,
			Computed:   got,
		}
	}
	return nil
}
