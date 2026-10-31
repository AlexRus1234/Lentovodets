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

// Каноническая форма конфигурации задания (docs/FORMAT.md §5.2): вход
// job_config_hash и поле job_config канонической формы индекса canon2.
// Схема та же, что у индекса: длина-префиксы вместо разделителей,
// числа десятичным ASCII. Резидентна в domain, потому что нужна и
// писателю (дедуп до записи), и адаптеру ленты (canon2).

package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// jobConfigPrefix — доменное разделение правила: хеш другого объекта с
// теми же полями не совпадёт с хешем конфигурации. Версия живёт в
// префиксе: добавление полей в Job поднимет canon2 внутри снапшота,
// не затрагивая формат ленты.
const jobConfigPrefix = "lentovodec.jobconfig.canon1\n"

// CanonicalJobConfig возвращает каноническую форму конфигурации
// задания: поля в фиксированном порядке, каждое — длина (uint64
// big-endian) + байты; списки — счётчик + элементы в исходном порядке.
// Порядок Paths/Exclude входит в форму: перестановка путей меняет и
// порядок записей tar, значит это другая конфигурация.
func CanonicalJobConfig(job Job) []byte {
	var buf bytes.Buffer
	buf.WriteString(jobConfigPrefix)
	field := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		buf.Write(n[:])
		buf.WriteString(s)
	}
	field(job.Name)
	field(job.Description)
	field(string(job.Mode))
	field(strconv.Itoa(len(job.Paths)))
	for _, p := range job.Paths {
		field(p)
	}
	field(strconv.Itoa(len(job.Exclude)))
	for _, e := range job.Exclude {
		field(e)
	}
	field(strconv.FormatInt(int64(job.SpanDepth), 10))
	return buf.Bytes()
}

// JobConfigHash — hex sha256 канонической формы конфигурации задания.
// Записывается в индекс каждой сессии (job_config_hash) и служит
// ключом дедупа: полное тело снапшота пишется только при изменении
// хеша относительно предыдущей сессии кассеты.
func JobConfigHash(job Job) string {
	sum := sha256.Sum256(CanonicalJobConfig(job))
	return hex.EncodeToString(sum[:])
}
