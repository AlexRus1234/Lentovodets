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
	"runtime"
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
	parts, err := domain.PlanSpan(files, 0, nil, 0)
	if err != nil {
		t.Fatalf("PlanSpan(budget=0): %v", err)
	}
	if len(parts) != 1 || len(parts[0]) != 2 {
		t.Fatalf("частей %d; want 1 часть с 2 файлами", len(parts))
	}
}

func TestPlanSpan_EmptyInput(t *testing.T) {
	parts, err := domain.PlanSpan(nil, 100, nil, 0)
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
	parts, err := domain.PlanSpan(files, 100, nil, 0)
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
	parts, err := domain.PlanSpan(files, 100, nil, 0)
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
	parts, err := domain.PlanSpan(files, 100, nil, 0)
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
	parts, err := domain.PlanSpan(files, 10, nil, 0)
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
	_, err := domain.PlanSpan(files, 100, nil, 0)
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

// TestPlanSpan_GroupBoundaryBeforeDirectory — depth=1: файл не влезает
// и его группа ещё не начата платно — граница части проходит перед
// группой, каталог-глава открывает следующую часть.
func TestPlanSpan_GroupBoundaryBeforeDirectory(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/tank/data/media"),
		file("/tank/data/media/a1", 60),
		dirF("/tank/data/docs"),
		file("/tank/data/docs/d1", 60),
	}
	parts, err := domain.PlanSpan(files, 100, []string{"/tank/data"}, 1)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/tank/data/media", "/tank/data/media/a1"},
		{"/tank/data/docs", "/tank/data/docs/d1"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

// TestPlanSpan_GroupRollbackToNextPart — файл не влезает, а его группа
// уже начата в части: откат — вся группа (с бесплатным каталогом)
// переносится в начало следующей части.
func TestPlanSpan_GroupRollbackToNextPart(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/r/b"),
		file("/r/b/g1", 30),
		dirF("/r/a"),
		file("/r/a/f1", 60),
		file("/r/a/f2", 40),
	}
	parts, err := domain.PlanSpan(files, 100, []string{"/r"}, 1)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/r/b", "/r/b/g1"},
		{"/r/a", "/r/a/f1", "/r/a/f2"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

// TestPlanSpan_GroupLargerThanBudgetFallsBackToFiles — группа целиком
// дороже бюджета: фолбэк на файловую резку внутри группы (каталог
// продолжает бекапиться), граница проходит по файлам.
func TestPlanSpan_GroupLargerThanBudgetFallsBackToFiles(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/r/b"),
		file("/r/b/g1", 30),
		dirF("/r/a"),
		file("/r/a/f1", 60),
		file("/r/a/f2", 50),
		file("/r/a/f3", 40),
	}
	parts, err := domain.PlanSpan(files, 100, []string{"/r"}, 1)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/r/b", "/r/b/g1", "/r/a", "/r/a/f1"},
		{"/r/a/f2", "/r/a/f3"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

// TestPlanSpan_FileTooLargeInsideFallbackGroup — негабаритный файл
// внутри группы дороже бюджета: тот же FileTooLargeError, что и без
// группировки.
func TestPlanSpan_FileTooLargeInsideFallbackGroup(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/r/a"),
		file("/r/a/f1", 150),
		file("/r/a/f2", 50),
	}
	_, err := domain.PlanSpan(files, 100, []string{"/r"}, 1)
	var tooBig *domain.FileTooLargeError
	if !errors.As(err, &tooBig) {
		t.Fatalf("PlanSpan: %v; want FileTooLargeError", err)
	}
	if tooBig.Path != "/r/a/f1" || tooBig.Size != 150 || tooBig.Capacity != 100 {
		t.Errorf("FileTooLargeError = %+v; want /r/a/f1 150/100", tooBig)
	}
}

// TestPlanSpan_DepthExceedsTreeHeight — depth больше высоты дерева:
// группа клэмпится к самому глубокому доступному предку, поддерево
// первого уровня остаётся одной группой (не режется по файлам).
func TestPlanSpan_DepthExceedsTreeHeight(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/tank/data/a"),
		file("/tank/data/a/f1", 60),
		file("/tank/data/a/f2", 40),
		file("/tank/data/a/f3", 30),
		dirF("/tank/data/b"),
		file("/tank/data/b/f4", 30),
	}
	parts, err := domain.PlanSpan(files, 150, []string{"/tank/data"}, 5)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/tank/data/a", "/tank/data/a/f1", "/tank/data/a/f2", "/tank/data/a/f3"},
		{"/tank/data/b", "/tank/data/b/f4"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

// TestPlanSpan_MultipleRoots — группы считаются в пределах своего
// корня задания: поддеревья разных корней не смешиваются.
func TestPlanSpan_MultipleRoots(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/tank/media"),
		file("/tank/media/m1", 60),
		dirF("/tank/docs"),
		file("/tank/docs/d1", 60),
		file("/tank/docs/d2", 40),
	}
	parts, err := domain.PlanSpan(files, 100, []string{"/tank/media", "/tank/docs"}, 1)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/tank/media", "/tank/media/m1"},
		{"/tank/docs", "/tank/docs/d1", "/tank/docs/d2"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

// TestPlanSpan_FreeEntriesRideWithGroup — tombstone и внутренний
// каталог группы бесплатны, не рвут её и при откате едут вместе
// с группой в следующую часть.
func TestPlanSpan_FreeEntriesRideWithGroup(t *testing.T) {
	files := []domain.FileMeta{
		dirF("/r/b"),
		file("/r/b/g1", 50),
		dirF("/r/a"),
		file("/r/a/f1", 50),
		tomb("/r/a/gone", 999),
		dirF("/r/a/sub"),
		file("/r/a/sub/s1", 40),
	}
	parts, err := domain.PlanSpan(files, 90, []string{"/r"}, 1)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/r/b", "/r/b/g1"},
		{"/r/a", "/r/a/f1", "/r/a/gone", "/r/a/sub", "/r/a/sub/s1"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

// TestPlanSpan_DepthZeroIgnoresRoots — depth=0 бит-в-бит текущее
// поведение: корни задания не влияют на резку по файлам.
func TestPlanSpan_DepthZeroIgnoresRoots(t *testing.T) {
	files := []domain.FileMeta{
		file("/r/a", 60), file("/r/b", 50), file("/r/c", 60),
	}
	plain, err := domain.PlanSpan(files, 100, nil, 0)
	if err != nil {
		t.Fatalf("PlanSpan(nil, 0): %v", err)
	}
	withRoots, err := domain.PlanSpan(files, 100, []string{"/r"}, 0)
	if err != nil {
		t.Fatalf("PlanSpan(roots, 0): %v", err)
	}
	if len(plain) != len(withRoots) {
		t.Fatalf("частей %d против %d; want равенство", len(plain), len(withRoots))
	}
	for i := range plain {
		if !equalStrings(partPaths(plain[i]), partPaths(withRoots[i])) {
			t.Errorf("часть %d: depth=0 %v ≠ depth=0+roots %v", i, partPaths(plain[i]), partPaths(withRoots[i]))
		}
	}
	if len(plain) != 3 {
		t.Fatalf("частей %d; want 3 (жадная файловая резка)", len(plain))
	}
}

// TestPlanSpan_FilesOutsideRootsOwnGroups — файлы вне корней задания
// (сканер так не ходит; защита планировщика) — каждый сам себе группа:
// файловая резка без отката; относительный путь без слэша — тоже.
func TestPlanSpan_FilesOutsideRootsOwnGroups(t *testing.T) {
	files := []domain.FileMeta{
		file("/x/y", 60),
		file("/x/z", 60),
		file("loose", 40),
	}
	parts, err := domain.PlanSpan(files, 100, []string{"/r"}, 1)
	if err != nil {
		t.Fatalf("PlanSpan: %v", err)
	}
	want := [][]string{
		{"/x/y"},
		{"/x/z", "loose"},
	}
	if len(parts) != len(want) {
		t.Fatalf("частей %d; want %d", len(parts), len(want))
	}
	for i, w := range want {
		if got := partPaths(parts[i]); !equalStrings(got, w) {
			t.Errorf("часть %d = %v; want %v", i, got, w)
		}
	}
}

func TestGroupKey(t *testing.T) {
	cases := []struct {
		path  string
		roots []string
		depth int32
		want  string
	}{
		// группировка выключена
		{"/tank/data/media/x", []string{"/tank/data"}, 0, ""},
		// сам корень — группа корневого уровня (0 уровней ниже)
		{"/tank/data", []string{"/tank/data"}, 1, "/tank/data"},
		{"/tank/data", []string{"/tank/data"}, 2, "/tank/data"},
		// depth 1: каталоги первого уровня под корнем
		{"/tank/data/media", []string{"/tank/data"}, 1, "/tank/data/media"},
		{"/tank/data/media/movies", []string{"/tank/data"}, 1, "/tank/data/media"},
		{"/tank/data/docs/guides/ops.md", []string{"/tank/data"}, 1, "/tank/data/docs"},
		// depth 2: каталоги второго уровня
		{"/tank/data/media/movies", []string{"/tank/data"}, 2, "/tank/data/media/movies"},
		{"/tank/data/media/movies/trilogy", []string{"/tank/data"}, 2, "/tank/data/media/movies"},
		// клэмп: цепочка короче depth — самый глубокой предок
		{"/tank/data/media", []string{"/tank/data"}, 7, "/tank/data/media"},
		// корень "/" матчит всё
		{"/etc/hosts.d", []string{"/"}, 1, "/etc"},
		{"/etc", []string{"/"}, 3, "/etc"},
		// вложенные корни: побеждает самый длинный (самый конкретный)
		{"/a/b/c", []string{"/a", "/a/b"}, 1, "/a/b/c"},
		{"/a/b/c/d", []string{"/a", "/a/b"}, 1, "/a/b/c"},
		{"/a/b/c/d", []string{"/a", "/a/b"}, 2, "/a/b/c/d"},
	}
	if runtime.GOOS == "windows" {
		// нормализация обратных слэшей — платформенное поведение
		// filepath.ToSlash; на Linux обратный слэш — часть имени,
		// кейс там не воспроизводим по построению
		cases = append(cases, struct {
			path  string
			roots []string
			depth int32
			want  string
		}{`C:\data\media\movies`, []string{`C:\data`}, 1, "C:/data/media"})
	}
	for _, tc := range cases {
		if got := domain.GroupKey(tc.path, tc.roots, tc.depth); got != tc.want {
			t.Errorf("GroupKey(%q, %v, %d) = %q; want %q", tc.path, tc.roots, tc.depth, got, tc.want)
		}
	}
	// вне корней задания — сам себе группа: ключи разных путей различны
	// и не совпадают с группами под корнями
	k1 := domain.GroupKey("/x/y", []string{"/a"}, 1)
	k2 := domain.GroupKey("/x/z", []string{"/a"}, 1)
	if k1 == "" || k2 == "" || k1 == k2 {
		t.Errorf("внекорневые группы: %q и %q; want непустые и различные", k1, k2)
	}
	if k1 == domain.GroupKey("/a/b", []string{"/a"}, 1) {
		t.Errorf("внекорневая группа %q совпала с корневой", k1)
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
