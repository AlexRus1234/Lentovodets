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
// См. логи/тома.md §2.2, план сессии 2 и сессию 9 (резка по каталогам).

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
//
// depth > 0 включает резку по границам каталогов (сессия 9): корни
// задания roots разбивают файлы на группы GroupKey, и части не рвут
// группу посередине — выгода локальность (поддерево целиком на одной
// кассете). Двухуровневый планировщик, порядок сканера сохраняется
// (детерминизм tar'а важнее упаковки):
//
//   - файл не влезает и начинает новую группу → граница части перед
//     его группой (каталог-глава группы открывает следующую часть);
//   - файл не влезает, а группа уже начата в части → откат: вся
//     группа переносится в начало следующей части (бесплатные
//     каталоги и tombstone'ы группы едут с ней);
//   - группа целиком дороже бюджета → фолбэк на файловую резку внутри
//     группы (иначе каталог перестал бы бекапиться вовсе);
//   - depth 0 — группы не используются, поведение бит-в-бит как
//     при резке по файлам.
func PlanSpan(files []FileMeta, budget int64, roots []string, depth int32) ([]SpanPart, error) {
	grouped := budget > 0 && depth > 0
	keys, totals := groupStats(files, roots, depth, grouped)

	st := spanState{groupStart: -1}
	for i := range files {
		cost := spanCost(files[i])
		if budget > 0 {
			if cost > budget {
				return nil, &FileTooLargeError{Path: files[i].Path, Size: files[i].Size, Capacity: budget}
			}
			if cost > budget-st.used {
				if grouped && keys[i] == st.curKey && st.groupStart >= 0 && totals[keys[i]] <= budget {
					// откат: сумма группы в бюджет влезает —
					// дополнений больше не потребуется
					st.rollbackGroup(keys[i], files[i])
					continue
				}
				st.cut(len(files))
			}
		}
		st.push(files[i], cost, keys[i], grouped)
	}
	return append(st.parts, st.cur), nil
}

// spanState — накопитель частей одного запуска PlanSpan.
type spanState struct {
	parts      []SpanPart // закрытые части
	cur        SpanPart   // набирающаяся часть
	used       int64      // расход бюджета cur
	curKey     string     // группа хвоста cur
	groupStart int        // индекс в cur, где группа curKey началась; -1 — не начата
}

// push добавляет запись в текущую часть; запись из другой группы
// начинает в ней новую группу.
func (st *spanState) push(fm FileMeta, cost int64, key string, grouped bool) {
	st.cur = append(st.cur, fm)
	st.used += cost
	if grouped && (st.groupStart < 0 || key != st.curKey) {
		st.curKey, st.groupStart = key, len(st.cur)-1
	}
}

// cut закрывает текущую часть и начинает пустую.
func (st *spanState) cut(capacity int) {
	st.parts = append(st.parts, st.cur)
	st.cur = make(SpanPart, 0, capacity)
	st.used = 0
	st.groupStart = -1
}

// rollbackGroup откатывает начатую в части группу: хвост до группы
// закрывается отдельной частью (если непуст), группа с записью fm
// переносится в начало следующей части.
func (st *spanState) rollbackGroup(key string, fm FileMeta) {
	if head := st.cur[:st.groupStart]; len(head) > 0 {
		st.parts = append(st.parts, head)
	}
	moved := make(SpanPart, 0, len(st.cur)-st.groupStart+1)
	moved = append(moved, st.cur[st.groupStart:]...)
	moved = append(moved, fm)
	st.cur = moved
	st.used = spanTotal(moved)
	st.curKey, st.groupStart = key, 0
}

// groupStats — ключи групп записей (entryGroup) и, при включённой
// группировке, суммы бюджета по группам (для фолбэка: группа целиком
// дороже бюджета режется по файлам без отката).
func groupStats(files []FileMeta, roots []string, depth int32, grouped bool) ([]string, map[string]int64) {
	keys := make([]string, len(files))
	totals := make(map[string]int64)
	for i := range files {
		keys[i] = entryGroup(files[i], roots, depth)
		if grouped {
			totals[keys[i]] += spanCost(files[i])
		}
	}
	return keys, totals
}

// GroupKey — группа пути при резке по каталогам: общий предок на
// depth уровнях вложенности под корнем задания, самым длинным
// совпавшим (если корни вложены — самый конкретный). Путь трактуется
// как каталог: каталог является собственной группой, файл группы не
// образует (в PlanSpan файлам передаётся родительский каталог, см.
// entryGroup). Цепочка короче depth клэмпится к самому глубокому
// предку (depth больше высоты дерева — поддерево целиком одна
// группа); сам корень задания — группа корневого уровня. depth ≤ 0 —
// группировки нет (""), путь вне корней задания — сам себе группа
// (префикс NUL не пересекается с путями).
func GroupKey(p string, roots []string, depth int32) string {
	if depth <= 0 {
		return ""
	}
	norm := NormalizePath(p)
	root := longestRoot(norm, roots)
	if root == "" {
		return "\x00" + norm
	}
	rel := relSegs(norm, root)
	if len(rel) == 0 {
		return root // сам корень — группа корневого уровня
	}
	if int32(len(rel)) > depth {
		rel = rel[:depth]
	}
	if root == "/" {
		return "/" + strings.Join(rel, "/")
	}
	return root + "/" + strings.Join(rel, "/")
}

// entryGroup — группа записи сессии: каталог принадлежит группе
// своего пути (глава группы — сам), файл и tombstone файла — группе
// родительского каталога; tombstone каталога — группе своего пути.
func entryGroup(fm FileMeta, roots []string, depth int32) string {
	if depth <= 0 {
		return ""
	}
	p := fm.Path
	if !fm.IsDir {
		p = parentDir(p)
	}
	return GroupKey(p, roots, depth)
}

// parentDir — родительский каталог пути (уже NormalizePath): без
// последнего сегмента; корень и одноsegmentный путь — "/".
func parentDir(p string) string {
	norm := NormalizePath(p)
	if i := strings.LastIndex(norm, "/"); i > 0 {
		return norm[:i]
	}
	return "/"
}

// longestRoot — самый длинный корень задания, которому принадлежит
// путь (сам путь или путь под ним); "" — не под одним корнем.
func longestRoot(p string, roots []string) string {
	best := ""
	for _, r := range roots {
		r = NormalizePath(r)
		if p == r || strings.HasPrefix(p, strings.TrimSuffix(r, "/")+"/") {
			if len(r) > len(best) {
				best = r
			}
		}
	}
	return best
}

// relSegs — сегменты пути под корнем (путь обязан быть корнем или
// лежать под ним); корень "/" отрезает ведущий слэш.
func relSegs(p, root string) []string {
	rel := p
	if root != "/" {
		rel = strings.TrimPrefix(rel, root)
	}
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

// spanTotal — суммарный расход бюджета части.
func spanTotal(part SpanPart) int64 {
	var total int64
	for _, fm := range part {
		total += spanCost(fm)
	}
	return total
}

// spanCost — расход бюджета файла: tombstone'ы и каталоги бесплатны.
func spanCost(fm FileMeta) int64 {
	if fm.IsDir || fm.IsDeleted() {
		return 0
	}
	return fm.Size
}

// NextTapeName предлагает имя следующей кассеты spanning-цепочки:
// инкремент числового суффикса текущего имени с сохранением ширины
// (media-013 → media-014, t-1 → t-2); имени без суффикса — <job>-002
// (пустое задание — текущее имя с суффиксом). Имя следующей кассеты
// должно быть решено до записи блока-указателя продолжения
// (UUID следующей кассеты неизвестен до её форматирования).
func NextTapeName(job, current string) string {
	i := len(current)
	for i > 0 && current[i-1] >= '0' && current[i-1] <= '9' {
		i--
	}
	if i < len(current) {
		if n, err := strconv.Atoi(current[i:]); err == nil {
			return fmt.Sprintf("%s%0*d", current[:i], len(current)-i, n+1)
		}
	}
	base := job
	if base == "" {
		base = current
	}
	return base + "-002"
}
