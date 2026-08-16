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

// Планировщик частей spanning-сессии: чистые функции без I/O.
// См. логи/тома.md §2.2 и план сессии 2.

package domain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseSize разбирает человекочитаемый размер конфига: `<мантисса><суффикс>`,
// суффиксы B, K/KiB, M/MiB, G/GiB, T/TiB (регистр не важен), степени 1024;
// суффикс можно опустить — байты. Мантисса допускает дробь: "2.2T".
// Результат округляется до целого байта.
func ParseSize(s string) (int64, error) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("ParseSize(%q): ожидался формат <число><суффикс>, мантисса отсутствует", s)
	}
	mant, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, fmt.Errorf("ParseSize(%q): битая мантисса: %w", s, err)
	}
	mult := sizeMultiplier(strings.ToLower(s[i:]))
	if mult == 0 {
		return 0, fmt.Errorf(
			"ParseSize(%q): неизвестный суффикс %q (допустимы B, K/KiB, M/MiB, G/GiB, T/TiB)", s, s[i:])
	}
	val := math.Round(mant * float64(mult))
	if val >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("ParseSize(%q): значение не помещается в int64", s)
	}
	return int64(val), nil
}

// sizeMultiplier — множитель суффикса (1024-степени); 0 — суффикс неизвестен.
func sizeMultiplier(suffix string) int64 {
	switch suffix {
	case "", "b":
		return 1
	case "k", "kib":
		return 1 << 10
	case "m", "mib":
		return 1 << 20
	case "g", "gib":
		return 1 << 30
	case "t", "tib":
		return 1 << 40
	default:
		return 0
	}
}

// SpanPart — файлы одной части сессии; порядок сканера сохраняется.
type SpanPart []FileMeta

// PlanSpan режет файлы сессии на части по бюджету кассеты.
//
// budget ≤ 0 — spanning выключен: одна часть без деления и проверок
// размера (переполнение ловит ENOSPC-путь записи). Иначе файлы в
// порядке сканера жадно набираются в часть: файл добавляется, пока
// остаток бюджета части его вмещает, иначе начинается новая часть.
// Каталоги и tombstone'ы бюджета не расходуют (на ленту не пишутся),
// но попадают в часть по порядку. Одиночный файл дороже бюджета —
// *FileTooLargeError. Результат содержит хотя бы одну часть (пустая
// сессия — одна пустая часть).
func PlanSpan(files []FileMeta, budget int64) ([]SpanPart, error) {
	parts := make([]SpanPart, 0, 1)
	cur := make(SpanPart, 0, len(files))
	var used int64
	for _, fm := range files {
		cost := spanCost(fm)
		if budget > 0 {
			if cost > budget {
				return nil, &FileTooLargeError{Path: fm.Path, Size: fm.Size, Capacity: budget}
			}
			if cost > budget-used {
				parts = append(parts, cur)
				cur = make(SpanPart, 0, len(files))
				used = 0
			}
		}
		cur = append(cur, fm)
		used += cost
	}
	return append(parts, cur), nil
}

// spanCost — расход бюджета файла: tombstone'ы и каталоги бесплатны.
func spanCost(fm FileMeta) int64 {
	if fm.IsDir || fm.IsDeleted() {
		return 0
	}
	return fm.Size
}
