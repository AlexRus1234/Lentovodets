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

const (
	// IndexHashAlgo — правило, которым sealIndex подписывает новые
	// индексы: canon3 покрывает per-file sha256 (ишью №4); canon2 —
	// снапшот конфигурации; canon1 — ленты до снапшота.
	IndexHashAlgo = "sha256-canon3"

	// indexHashAlgoCanon2 — правило лент, записанных со снапшотом
	// конфигурации до per-file sha256 (8e54f41): per-file sha256 в
	// форму canon2 не входит.
	indexHashAlgoCanon2 = "sha256-canon2"

	// indexHashAlgoCanon1 — правило лент, записанных до снапшота
	// (4a835af): снапшотные поля в canon1 не входят.
	indexHashAlgoCanon1 = "sha256-canon1"
)

// canonicalPrefix — доменное разделение правила по версии: хеш
// другого объекта с теми же полями не совпадёт с индексным.
const (
	canonicalPrefixV3 = "lentovodec.index.canon3\n"
	canonicalPrefixV2 = "lentovodec.index.canon2\n"
	canonicalPrefixV1 = "lentovodec.index.canon1\n"
)

// canonicalIndex возвращает каноническую форму индекса по правилу
// algo: поля в фиксированном порядке, каждое — длина (uint64
// big-endian) + байты. Длина вместо разделителей снимает вопрос
// экранирования: имя файла на Linux может содержать любые байты,
// кроме NUL и '/'. Числа — десятичный ASCII, bool — "0"/"1", пустая
// строка — длина 0. canon2 добавляет после continues поля снапшота
// конфигурации (docs/FORMAT.md §5.2); canon3 — ещё и per-file sha256
// после hash; canon1 — форма лент до снапшота.
func canonicalIndex(idx *SessionIndex, algo string) []byte {
	var buf bytes.Buffer
	switch algo {
	case indexHashAlgoCanon1:
		buf.WriteString(canonicalPrefixV1)
	case indexHashAlgoCanon2:
		buf.WriteString(canonicalPrefixV2)
	default:
		buf.WriteString(canonicalPrefixV3)
	}
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
	if algo != indexHashAlgoCanon1 {
		writeField(idx.ToolVersion)
		writeField(idx.ConfigSource)
		writeField(strconv.FormatInt(idx.ConfigMTime, 10))
		writeField(idx.JobConfigHash)
		writeField(string(jobConfigField(idx.JobConfig)))
	}
	writeField(strconv.Itoa(len(idx.Files)))
	for i := range idx.Files {
		fm := &idx.Files[i]
		writeField(fm.Path)
		writeField(strconv.FormatInt(fm.Size, 10))
		writeField(strconv.FormatInt(fm.ModTime, 10))
		writeField(boolField(fm.IsDir))
		writeField(fm.Hash)
		if algo == IndexHashAlgo {
			writeField(fm.SHA256)
		}
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

// jobConfigField — канонические байты тела снапшота одним полем
// canon2-формы: sha256 этого поля равен job_config_hash, а целостность
// тела покрывается index_sha256 вместе со всем индексом. nil-снапшот
// (дедуп, старая лента) — пустое поле длины 0.
func jobConfigField(s *JobSnapshot) []byte {
	if s == nil {
		return nil
	}
	return domain.CanonicalJobConfig(*s.job())
}

// indexDigest — hex sha256 канонической формы индекса по правилу algo.
func indexDigest(idx *SessionIndex, algo string) string {
	sum := sha256.Sum256(canonicalIndex(idx, algo))
	return hex.EncodeToString(sum[:])
}

// sealIndex проставляет правило и контрольную сумму перед записью.
func sealIndex(idx *SessionIndex) {
	idx.HashAlgo = IndexHashAlgo
	idx.IndexSHA256 = indexDigest(idx, IndexHashAlgo)
}

// verifyIndexHash проверяет контрольную сумму разобранного индекса до
// нормализации и использования полей. Отсутствие поля — лента записана
// до появления КС: проверка не выполняется. Незнакомое правило — индекс
// записан более новым бинарём: проверить нечем, чтение не блокируется
// (DR важнее обнаружения подмены; см. docs/FORMAT.md §5.2). Ленты
// canon1/canon2 проверяются по своим правилам, как раньше.
func verifyIndexHash(idx *SessionIndex) error {
	if idx.IndexSHA256 == "" {
		return nil
	}
	switch idx.HashAlgo {
	case IndexHashAlgo, indexHashAlgoCanon2, indexHashAlgoCanon1:
	default:
		return nil
	}
	if got := indexDigest(idx, idx.HashAlgo); got != idx.IndexSHA256 {
		return &domain.IndexCorruptError{
			SessionNum: idx.SessionNum,
			Algo:       idx.HashAlgo,
			Recorded:   idx.IndexSHA256,
			Computed:   got,
		}
	}
	return nil
}
