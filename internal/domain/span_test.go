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

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"lentovodec/internal/domain"
)

func TestParseSize(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"100B", 100, false},
		{"100", 100, false},                      // суффикс опущен — байты
		{"1K", 1 << 10, false},                   //
		{"1k", 1 << 10, false},                   //
		{"1KiB", 1 << 10, false},                 //
		{"1kib", 1 << 10, false},                 //
		{"16K", 16 << 10, false},                 //
		{"1.5M", 1572864, false},                 //
		{"512MiB", 512 << 20, false},             //
		{".5G", 536870912, false},                // дробь без ведущего нуля
		{"1G", 1 << 30, false},                   //
		{"2.2T", 2418925581107, false},           // рекомендация для LTO-6
		{"8388607T", 9223370937343148032, false}, // 2^63 − 2^40: влезает в int64
		{"0B", 0, false},                         //
		{"", 0, true},                            // пусто
		{"abc", 0, true},                         // нет мантиссы
		{"-5G", 0, true},                         // минус не входит в мантиссу
		{".", 0, true},                           // битая мантисса
		{"1.2.3G", 0, true},                      // битая мантисса
		{"10X", 0, true},                         // неизвестный суффикс
		{"1 K", 0, true},                         // пробел не входит в суффикс
		{"9223372036854775808G", 0, true},        // переполнение int64
	}
	for _, tc := range cases {
		got, err := domain.ParseSize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseSize(%q) = %d; want ошибка", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSize(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSize(%q) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

// file — обычный файл размера size.
func file(path string, size int64) domain.FileMeta {
	return domain.FileMeta{Path: path, Size: size, State: domain.StateAdded}
}

// dirF — каталог (Size=0).
func dirF(path string) domain.FileMeta {
	return domain.FileMeta{Path: path, IsDir: true, State: domain.StateAdded}
}

// tomb — tombstone удалённого файла; Size хранит прошлый размер,
// но на ленту запись не попадает.
func tomb(path string, size int64) domain.FileMeta {
	return domain.FileMeta{Path: path, Size: size, State: domain.StateDeleted}
}

// partPaths — пути части в порядке следования.
func partPaths(p domain.SpanPart) []string {
	paths := make([]string, len(p))
	for i, fm := range p {
		paths[i] = fm.Path
	}
	return paths
}

func TestPlanSpan_DisabledSinglePart(t *testing.T) {
	// budget ≤ 0 — spanning выключен: одна часть, размеры не проверяются.
	files := []domain.FileMeta{file("a", 1<<40), file("b", 5)}
	parts, err := domain.PlanSpan(files, 0)
	if err != nil {
		t.Fatalf("PlanSpan(budget=0): %v", err)
	}
	if len(parts) != 1 || len(parts[0]) != 2 {
		t.Fatalf("частей %d; want 1 часть с 2 файлами", len(parts))
	}
}

func TestPlanSpan_EmptyInput(t *testing.T) {
	parts, err := domain.PlanSpan(nil, 100)
	if err != nil {
		t.Fatalf("PlanSpan(nil): %v", err)
	}
	if len(parts) != 1 || len(parts[0]) != 0 {
		t.Fatalf("частей %d; want 1 пустая часть", len(parts))
	}
}

func TestPlanSpan_ExactFitSinglePart(t *testing.T) {
	// сумма ровно в бюджет — одна часть.
	files := []domain.FileMeta{file("a", 50), file("b", 50)}
	parts, err := domain.PlanSpan(files, 100)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("частей %d; want 1", len(parts))
	}
}

func TestPlanSpan_GreedySplitInOrder(t *testing.T) {
	// жадный набор в порядке сканера, без пересортировки «большие вперёд».
	files := []domain.FileMeta{file("a", 60), file("b", 50), file("c", 60)}
	parts, err := domain.PlanSpan(files, 100)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("частей %d; want 3", len(parts))
	}
	want := [][]string{{"a"}, {"b"}, {"c"}}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

func TestPlanSpan_BoundaryStartsNewPart(t *testing.T) {
	// заполнение части ровно до бюджета: следующий файл — новая часть.
	files := []domain.FileMeta{file("a", 60), file("b", 40), file("c", 1)}
	parts, err := domain.PlanSpan(files, 100)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("частей %d; want 2", len(parts))
	}
	if got := partPaths(parts[0]); !equalStrings(got, []string{"a", "b"}) {
		t.Errorf("часть 0 = %v; want [a b]", got)
	}
	if got := partPaths(parts[1]); !equalStrings(got, []string{"c"}) {
		t.Errorf("часть 1 = %v; want [c]", got)
	}
}

func TestPlanSpan_DirsAndTombstonesFree(t *testing.T) {
	// каталоги и tombstone'ы не расходуют бюджет, но идут по порядку;
	// Size tombstone'а больше бюджета — не ошибка (на ленту не пишется).
	files := []domain.FileMeta{
		dirF("d1"), file("a", 10), tomb("gone", 999), dirF("d2"),
	}
	parts, err := domain.PlanSpan(files, 10)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("частей %d; want 1 (бесплатные записи не режут)", len(parts))
	}
	if got := partPaths(parts[0]); !equalStrings(got, []string{"d1", "a", "gone", "d2"}) {
		t.Errorf("часть 0 = %v; want порядок сканера", got)
	}
}

func TestPlanSpan_FileTooLarge(t *testing.T) {
	files := []domain.FileMeta{file("a", 60), file("/big", 101)}
	_, err := domain.PlanSpan(files, 100)
	var tooBig *domain.FileTooLargeError
	if !errors.As(err, &tooBig) {
		t.Fatalf("PlanSpan: %v; want FileTooLargeError", err)
	}
	if tooBig.Path != "/big" || tooBig.Size != 101 || tooBig.Capacity != 100 {
		t.Errorf("FileTooLargeError = %+v; want /big 101/100", tooBig)
	}
	if !errors.Is(err, &domain.FileTooLargeError{}) {
		t.Error("errors.Is(err, &FileTooLargeError{}) = false")
	}
	if !strings.Contains(err.Error(), "/big") {
		t.Errorf("текст ошибки %q не называет файл", err)
	}
}

// equalStrings — поэлементное сравнение.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNextTapeName(t *testing.T) {
	cases := []struct {
		job, current, want string
	}{
		{"daily", "media-013", "media-014"},            // инкремент, ширина сохранена
		{"daily", "media-009", "media-010"},            // ширина 3 не сужается
		{"daily", "media-999", "media-1000"},           // расширение на переносе
		{"daily", "t-1", "t-2"},                        //
		{"daily", "T1", "T2"},                          //
		{"daily", "123", "124"},                        // имя целиком из цифр
		{"daily", "media-", "daily-002"},               // суффикс пустой — не цифры
		{"daily", "backup", "daily-002"},               // суффикса нет
		{"", "backup", "backup-002"},                   // задание не задано — от текущего
		{"daily", "99999999999999999999", "daily-002"}, // переполнение Atoi
	}
	for _, tc := range cases {
		if got := domain.NextTapeName(tc.job, tc.current); got != tc.want {
			t.Errorf("NextTapeName(%q, %q) = %q; want %q", tc.job, tc.current, got, tc.want)
		}
	}
}
