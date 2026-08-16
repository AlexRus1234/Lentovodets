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

package filetape_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lentovodec/internal/adapter/filetape"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// openTape открывает ленту или падает.
func openTape(t *testing.T, path string) *filetape.Tape {
	t.Helper()
	tp, err := filetape.Open(path)
	if err != nil {
		t.Fatalf("filetape.Open(%q): %v", path, err)
	}
	return tp
}

// writeBlock пишет блок или падает.
func writeBlock(t *testing.T, tp port.Tape, data string) {
	t.Helper()
	if err := tp.WriteBlock(context.Background(), []byte(data)); err != nil {
		t.Fatalf("WriteBlock(%q): %v", data, err)
	}
}

// writeEOF ставит filemark или падает.
func writeEOF(t *testing.T, tp port.Tape) {
	t.Helper()
	if err := tp.WriteEOF(context.Background()); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
}

// wantBlock читает блок и сверяет его с ожидаемым (с учётом добивки
// нулями до domain.BlockSize).
func wantBlock(t *testing.T, tp port.Tape, want string) {
	t.Helper()
	block, err := tp.ReadBlock(context.Background())
	if err != nil {
		t.Fatalf("ReadBlock: ожидался блок %q, получена ошибка %v", want, err)
	}
	if len(block) != domain.BlockSize {
		t.Fatalf("ReadBlock: длина блока %d, хочу %d", len(block), domain.BlockSize)
	}
	if string(block[:len(want)]) != want {
		t.Fatalf("ReadBlock: префикс %q, хочу %q", block[:len(want)], want)
	}
	for i, b := range block[len(want):] {
		if b != 0 {
			t.Fatalf("ReadBlock: байт добивки %d = 0x%02x, хочу 0", i+len(want), b)
		}
	}
}

// wantEOF читает и требует io.EOF.
func wantEOF(t *testing.T, tp port.Tape) {
	t.Helper()
	if _, err := tp.ReadBlock(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadBlock: хочу io.EOF, получил %v", err)
	}
}

// wantErr проверяет, что операция вернула ошибку с подстрокой want.
func wantErr(t *testing.T, desc, want string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: хочу ошибку %q, получил nil", desc, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: ошибка %q не содержит %q", desc, err, want)
	}
}

func TestOpen_NewFileIsEmptyTapeAtBOT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openTape(t, path)
	defer tp.Close()

	// Файл начинается с magic.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasPrefix(string(raw), "FTAPEV1\x00") {
		t.Fatalf("файл не начинается с magic: %q", raw[:16])
	}
	// Новая лента пуста: чтение в BOT даёт io.EOF.
	wantEOF(t, tp)
}

func TestOpen_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openTape(t, path)
	writeBlock(t, tp, "alpha")
	writeEOF(t, tp)
	writeBlock(t, tp, "beta")
	writeEOF(t, tp)
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tp2 := openTape(t, path)
	defer tp2.Close()
	// Открытие ставит позицию в BOT.
	wantBlock(t, tp2, "alpha")
	wantEOF(t, tp2)
	wantBlock(t, tp2, "beta")
	wantEOF(t, tp2)
	wantEOF(t, tp2)
}

func TestOpen_InvalidMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	if err := os.WriteFile(path, []byte("NOT A TAPE AT ALL"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := filetape.Open(path)
	if err == nil {
		t.Fatal("Open: хочу ошибку для файла с чужим содержимым")
	}
	var itf *filetape.InvalidTapeFileError
	if !errors.As(err, &itf) {
		t.Fatalf("Open: ошибка %v не InvalidTapeFileError", err)
	}
	if !errors.Is(err, &filetape.InvalidTapeFileError{}) {
		t.Fatal("Open: errors.Is(InvalidTapeFileError) == false")
	}
	if itf.Path != path {
		t.Fatalf("InvalidTapeFileError.Path = %q, хочу %q", itf.Path, path)
	}
	if msg := (&filetape.InvalidTapeFileError{Path: "/x/tape.dat"}).Error(); !strings.Contains(msg, "/x/tape.dat") {
		t.Fatalf("Error(): %q не содержит путь", msg)
	}
}

func TestOpen_TruncatedMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	if err := os.WriteFile(path, []byte("FTA"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := filetape.Open(path)
	wantErr(t, "Open(обрубленный magic)", "чтение magic", err)
}

func TestOpen_NotARegularFile(t *testing.T) {
	// Открытие каталога с O_RDWR запрещено и на linux, и на windows.
	_, err := filetape.Open(t.TempDir())
	if err == nil {
		t.Fatal("Open(каталог): хочу ошибку")
	}
}

func TestOpen_MissingDirectory(t *testing.T) {
	_, err := filetape.Open(filepath.Join(t.TempDir(), "no-such-dir", "tape.dat"))
	if err == nil {
		t.Fatal("Open(несуществующий каталог): хочу ошибку")
	}
}

func TestWriteBlock_PadsShortBlock(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "hi")
	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "hi")
}

func TestWriteBlock_EmptyBlockBecomesFullZeroBlock(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	if err := tp.WriteBlock(context.Background(), nil); err != nil {
		t.Fatalf("WriteBlock(nil): %v", err)
	}
	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "")
}

func TestWriteBlock_OversizeRejected(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	big := make([]byte, domain.BlockSize+1)
	err := tp.WriteBlock(context.Background(), big)
	wantErr(t, "WriteBlock(размер > BlockSize)", "больше BlockSize", err)
}

func TestWriteEOF_TruncatesTail(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "a1")
	writeEOF(t, tp)
	writeBlock(t, tp, "a2")
	writeEOF(t, tp)

	// В BOT, затем за первый filemark (на блок a2) и поставить метку:
	// хвост (a2 и вторая метка) уничтожается.
	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if err := tp.ForwardFilemarks(context.Background(), 1); err != nil {
		t.Fatalf("ForwardFilemarks: %v", err)
	}
	writeEOF(t, tp)

	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "a1")
	wantEOF(t, tp) // первая метка
	wantEOF(t, tp) // новая метка
	wantEOF(t, tp) // EOD
}

func TestWriteBlock_TruncatesTail(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "old1")
	writeEOF(t, tp)
	writeBlock(t, tp, "old2")
	writeEOF(t, tp)

	// Перезапись с BOT (после записей позиция — EOD) затирает всё после magic.
	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	writeBlock(t, tp, "new1")
	writeEOF(t, tp)

	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "new1")
	wantEOF(t, tp)
	wantEOF(t, tp)
}

func TestForwardFilemarks_Navigation(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "s1-index")
	writeEOF(t, tp)
	writeBlock(t, tp, "s1-tar")
	writeEOF(t, tp)
	writeBlock(t, tp, "s2-index")
	writeEOF(t, tp)

	cases := []struct {
		n    int
		want string
	}{
		{1, "s1-tar"}, // MTFSF(1) — начало tar сессии 1 (FORMAT §9)
		{2, "s2-index"},
	}
	ctx := context.Background()
	for _, tc := range cases {
		if err := tp.Rewind(ctx); err != nil {
			t.Fatalf("Rewind: %v", err)
		}
		if err := tp.ForwardFilemarks(ctx, tc.n); err != nil {
			t.Fatalf("ForwardFilemarks(%d): %v", tc.n, err)
		}
		wantBlock(t, tp, tc.want)
	}

	// Недостаточно filemark'ов вперёд.
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	err := tp.ForwardFilemarks(ctx, 99)
	wantErr(t, "ForwardFilemarks(99)", "вперёд доступно только", err)
	// Позиция не изменилась: чтение даёт первый блок.
	wantBlock(t, tp, "s1-index")

	for _, n := range []int{0, -1} {
		err := tp.ForwardFilemarks(ctx, n)
		wantErr(t, "ForwardFilemarks(n<=0)", "count должен быть > 0", err)
	}
}

func TestBackwardFilemarks_Navigation(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "f1")
	writeEOF(t, tp)
	writeBlock(t, tp, "f2")
	writeEOF(t, tp)
	writeBlock(t, tp, "f3")
	writeEOF(t, tp)

	ctx := context.Background()
	// С начала ленты назад меток нет.
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	err := tp.BackwardFilemarks(ctx, 1)
	wantErr(t, "BackwardFilemarks из BOT", "назад доступно только 0", err)

	// Семантика «сразу после n-го сзади filemark'а»: из EOD лента стоит
	// уже за последней меткой, поэтому MTBSFM(1) — no-op (позиция не
	// меняется), а MTBSFM(2) встаёт на начало f3.
	if err := tp.EndOfData(ctx); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	if err := tp.BackwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("BackwardFilemarks(1) из EOD: %v", err)
	}
	wantEOF(t, tp) // всё ещё EOD

	if err := tp.BackwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("BackwardFilemarks(2): %v", err)
	}
	wantBlock(t, tp, "f3")

	// MTBSFM(2) от начала f3 → через m2 назад к m1 → начало f2.
	if err := tp.BackwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("BackwardFilemarks(2) от f3: %v", err)
	}
	wantBlock(t, tp, "f2")

	if err := tp.BackwardFilemarks(ctx, 99); err == nil {
		t.Fatal("BackwardFilemarks(99): хочу ошибку")
	}
	err = tp.BackwardFilemarks(ctx, 0)
	wantErr(t, "BackwardFilemarks(0)", "count должен быть > 0", err)
}

func TestEndOfData_AppendNewSession(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "first")
	writeEOF(t, tp)
	// Дозапись: EOD → новая сессия.
	if err := tp.EndOfData(context.Background()); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	writeBlock(t, tp, "second")
	writeEOF(t, tp)

	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "first")
	wantEOF(t, tp)
	wantBlock(t, tp, "second")
	wantEOF(t, tp)
	wantEOF(t, tp)
}

func TestEndOfData_OverwriteFromEOD(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "keep")
	writeEOF(t, tp)
	writeBlock(t, tp, "stale")
	writeEOF(t, tp)

	// EOD, затем на две метки назад (EOD стоит уже за последней меткой
	// — начало stale) и перезапись.
	ctx := context.Background()
	if err := tp.EndOfData(ctx); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	if err := tp.BackwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("BackwardFilemarks(2): %v", err)
	}
	writeBlock(t, tp, "fresh")
	writeEOF(t, tp)

	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "keep")
	wantEOF(t, tp)
	wantBlock(t, tp, "fresh")
	wantEOF(t, tp)
	wantEOF(t, tp)
}

func TestEject_NoOpKeepsTapeUsable(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()

	writeBlock(t, tp, "data")
	if err := tp.Eject(context.Background()); err != nil {
		t.Fatalf("Eject: %v", err)
	}
	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "data")
}

func TestClosedTape_OperationsFail(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	writeBlock(t, tp, "data")
	writeEOF(t, tp)
	// В BOT, чтобы операции после Close реально трогали файл.
	if err := tp.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx := context.Background()
	_, rerr := tp.ReadBlock(ctx)
	if rerr == nil || errors.Is(rerr, io.EOF) {
		t.Fatalf("ReadBlock после Close: хочу ошибку, получил %v", rerr)
	}
	if err := tp.WriteBlock(ctx, []byte("x")); err == nil {
		t.Fatal("WriteBlock после Close: хочу ошибку")
	}
	if err := tp.WriteEOF(ctx); err == nil {
		t.Fatal("WriteEOF после Close: хочу ошибку")
	}
	if err := tp.ForwardFilemarks(ctx, 1); err == nil {
		t.Fatal("ForwardFilemarks после Close: хочу ошибку")
	}
	if err := tp.BackwardFilemarks(ctx, 1); err == nil {
		t.Fatal("BackwardFilemarks после Close: хочу ошибку")
	}
	if err := tp.Close(); err == nil {
		t.Fatal("двойной Close: хочу ошибку")
	}

	// Позиция EOD (pos == size): усечение не нужно, ошибка — из WriteAt.
	tp2 := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	writeBlock(t, tp2, "data")
	writeEOF(t, tp2)
	if err := tp2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tp2.WriteBlock(ctx, []byte("x")); err == nil {
		t.Fatal("WriteBlock после Close (EOD): хочу ошибку")
	}
	if err := tp2.WriteEOF(ctx); err == nil {
		t.Fatal("WriteEOF после Close (EOD): хочу ошибку")
	}
}

func TestCorruptFile_UnknownRecordType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openTape(t, path)
	writeBlock(t, tp, "data")
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Портить тип первой записи.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, 8); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	f.Close()

	tp2 := openTape(t, path)
	defer tp2.Close()
	_, rerr := tp2.ReadBlock(context.Background())
	wantErr(t, "ReadBlock(битый тип записи)", "неизвестный тип записи", rerr)

	// EOD переносит позицию за битую запись: BackwardFilemarks
	// встречает её при сканировании от BOT.
	if err := tp2.EndOfData(context.Background()); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	if err := tp2.BackwardFilemarks(context.Background(), 1); err == nil {
		t.Fatal("BackwardFilemarks(битая запись): хочу ошибку")
	}
}

func TestCorruptFile_RecordBeyondEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openTape(t, path)
	writeBlock(t, tp, "data")
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Отрезать 3 байта payload: заголовок обещает больше, чем есть.
	if err := os.Truncate(path, st.Size()-3); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	tp2 := openTape(t, path)
	defer tp2.Close()
	_, err = tp2.ReadBlock(context.Background())
	wantErr(t, "ReadBlock(запись за концом)", "выходит за конец данных", err)
}

func TestCorruptFile_TruncatedHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openTape(t, path)
	writeBlock(t, tp, "data")
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Оставить magic + 2 байта: заголовок записи нечитаем.
	if err := os.Truncate(path, 10); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	tp2 := openTape(t, path)
	defer tp2.Close()
	_, rerr := tp2.ReadBlock(context.Background())
	wantErr(t, "ReadBlock(обрубленный заголовок)", "чтение заголовка записи", rerr)
}

func TestContextCanceled_AllOperationsStop(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()
	writeBlock(t, tp, "data")
	writeEOF(t, tp)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tp.ReadBlock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadBlock: %v, хочу context.Canceled", err)
	}
	ops := map[string]func() error{
		"WriteBlock":        func() error { return tp.WriteBlock(ctx, []byte("x")) },
		"WriteEOF":          func() error { return tp.WriteEOF(ctx) },
		"ForwardFilemarks":  func() error { return tp.ForwardFilemarks(ctx, 1) },
		"BackwardFilemarks": func() error { return tp.BackwardFilemarks(ctx, 1) },
		"Rewind":            func() error { return tp.Rewind(ctx) },
		"EndOfData":         func() error { return tp.EndOfData(ctx) },
		"Eject":             func() error { return tp.Eject(ctx) },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v, хочу context.Canceled", name, err)
		}
	}
}

func TestTape_ImplementsPortTape(t *testing.T) {
	tp := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer tp.Close()
	var _ port.Tape = tp
}

// openCapTape открывает ленту с лимитом ёмкости или падает.
func openCapTape(t *testing.T, path string, capacity int64) *filetape.Tape {
	t.Helper()
	tp, err := filetape.OpenCapacity(path, capacity)
	if err != nil {
		t.Fatalf("filetape.OpenCapacity(%q, %d): %v", path, capacity, err)
	}
	return tp
}

func TestOpenCapacity_BlocksAndFilemarksCounted(t *testing.T) {
	capB := int64(2*domain.BlockSize + 2)
	tp := openCapTape(t, filepath.Join(t.TempDir(), "tape.dat"), capB)
	defer tp.Close()
	ctx := context.Background()

	writeBlock(t, tp, "one")
	writeBlock(t, tp, "two")
	writeEOF(t, tp)
	writeEOF(t, tp) // 2*BlockSize + 2 — ровно на границе лимита, влезает

	wantErr(t, "WriteBlock за лимитом", "no space left on device",
		tp.WriteBlock(ctx, []byte("over")))
	wantErr(t, "WriteEOF за лимитом", "no space left on device",
		tp.WriteEOF(ctx))

	// отклонённые записи не оставили следов: лента читается до конца
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "one")
	wantBlock(t, tp, "two")
	wantEOF(t, tp)
	wantEOF(t, tp)
	wantEOF(t, tp) // EOD
}

func TestOpenCapacity_RejectedWriteKeepsTape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openCapTape(t, path, int64(domain.BlockSize+1))
	defer tp.Close()
	ctx := context.Background()

	writeBlock(t, tp, "only")
	writeEOF(t, tp) // лимит исчерпан
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat до отказа: %v", err)
	}

	wantErr(t, "WriteBlock за лимитом", "no space left on device",
		tp.WriteBlock(ctx, []byte("x")))
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat после отказа: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("отказ изменил файл: %d → %d байт", before.Size(), after.Size())
	}

	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "only")
	wantEOF(t, tp) // filemark
	wantEOF(t, tp) // EOD
}

// TestOpenCapacity_TruncateFreesSpaceForRewrite — сценарий ENOSPC-отката
// (сессия 1 сессии-00/тома §2.3): запись сверх лимита отклоняется, откат
// Rewind+FSF+WEOF×2 усекает грязный хвост, место освобождается для
// дозаписи.
func TestOpenCapacity_TruncateFreesSpaceForRewrite(t *testing.T) {
	capB := int64(2*domain.BlockSize + 4)
	tp := openCapTape(t, filepath.Join(t.TempDir(), "tape.dat"), capB)
	defer tp.Close()
	ctx := context.Background()

	writeBlock(t, tp, "s1")
	writeEOF(t, tp)            // конец сессии 1
	writeBlock(t, tp, "dirty") // неудавшаяся запись…
	writeEOF(t, tp)            // …её метка
	writeEOF(t, tp)            // EOD-пара
	wantErr(t, "блок сверх ёмкости", "no space left on device",
		tp.WriteBlock(ctx, []byte("more")))

	// откат к старому EOD: позиция за меткой сессии 1, пара WEOF
	// усекает грязный хвост (запись с позиции уничтожает остаток).
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if err := tp.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("ForwardFilemarks: %v", err)
	}
	writeEOF(t, tp)
	writeEOF(t, tp)

	// дозапись в освободившееся место проходит до ровно лимита
	writeBlock(t, tp, "s2")
	writeEOF(t, tp)

	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	wantBlock(t, tp, "s1")
	wantEOF(t, tp) // метка сессии 1
	wantEOF(t, tp) // EOD-пара отката
	wantEOF(t, tp)
	wantBlock(t, tp, "s2")
	wantEOF(t, tp) // метка сессии 2
	wantEOF(t, tp) // EOD
}

func TestOpenCapacity_ReopenRecountsUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	capB := int64(2*domain.BlockSize + 1)
	tp := openCapTape(t, path, capB)
	writeBlock(t, tp, "a")
	writeBlock(t, tp, "b") // 2*BlockSize — весь лимит без запаса на блок
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tp2 := openCapTape(t, path, capB)
	defer tp2.Close()
	ctx := context.Background()
	if err := tp2.EndOfData(ctx); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	writeEOF(t, tp2) // 2*BlockSize+1 — влезает ровно: переоткрытие всё посчитало
	wantErr(t, "WriteBlock после переоткрытия", "no space left on device",
		tp2.WriteBlock(ctx, []byte("c")))

	// Open без лимита на том же файле — ёмкость бесконечна
	tp3 := openTape(t, path)
	defer tp3.Close()
	if err := tp3.EndOfData(ctx); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	writeBlock(t, tp3, "c")
}

func TestOpenCapacity_InvalidCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	for _, capB := range []int64{0, -1} {
		_, err := filetape.OpenCapacity(path, capB)
		wantErr(t, fmt.Sprintf("OpenCapacity(%d)", capB), "capacity должен быть > 0", err)
	}
}

func TestOpenCapacity_CorruptRecordsFailOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.dat")
	tp := openTape(t, path)
	writeBlock(t, tp, "data")
	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// испортить тип первой записи: подсчёт ёмкости не пройдёт
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, 8); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	f.Close()

	if _, err := filetape.OpenCapacity(path, int64(domain.BlockSize)); err == nil {
		t.Fatal("OpenCapacity(битые записи): хочу ошибку подсчёта ёмкости")
	}
}

// TestEquivalenceWithFakeTape прогоняет один сценарий (запись, дозапись
// в EOD, навигация назад, перезапись хвоста, полный проход) на filetape
// и FakeTape и сверяет результирующие последовательности.
func TestEquivalenceWithFakeTape(t *testing.T) {
	events := func(tp port.Tape) []string {
		ctx := context.Background()
		var out []string
		do := func(op string, err error) {
			if err != nil {
				out = append(out, "ERR "+op+": "+err.Error())
			}
		}
		do("WriteBlock", tp.WriteBlock(ctx, []byte("AAAA")))
		do("WriteEOF", tp.WriteEOF(ctx))
		do("WriteBlock", tp.WriteBlock(ctx, []byte("BBBB")))
		do("WriteBlock", tp.WriteBlock(ctx, []byte("CCCC")))
		do("WriteEOF", tp.WriteEOF(ctx))
		do("EndOfData", tp.EndOfData(ctx))
		do("WriteBlock", tp.WriteBlock(ctx, []byte("DDDD")))
		do("WriteEOF", tp.WriteEOF(ctx))
		// Назад за две метки от EOD → начало BBBB, перезапись хвоста.
		do("BackwardFilemarks", tp.BackwardFilemarks(ctx, 2))
		do("WriteBlock", tp.WriteBlock(ctx, []byte("XXXX")))
		do("WriteEOF", tp.WriteEOF(ctx))
		// Полный проход с BOT до EOD (два io.EOF подряд).
		do("Rewind", tp.Rewind(ctx))
		eofs := 0
		for eofs < 2 {
			block, err := tp.ReadBlock(ctx)
			switch {
			case errors.Is(err, io.EOF):
				eofs++
				out = append(out, "EOF")
			case err != nil:
				out = append(out, "ERR ReadBlock: "+err.Error())
				return out
			default:
				eofs = 0
				out = append(out, "BLOCK "+string(bytes.TrimRight(block, "\x00")))
			}
		}
		return append(out, "EOD")
	}

	ft := openTape(t, filepath.Join(t.TempDir(), "tape.dat"))
	defer ft.Close()
	gotFile := events(ft)
	gotFake := events(testutil.NewFakeTape())

	want := []string{
		"BLOCK AAAA", "EOF", // filemark после первой записи
		"BLOCK BBBB", "BLOCK CCCC", "EOF", // сессия из двух блоков
		"BLOCK XXXX", "EOF", // перезаписанный хвост с MTBSFM-позиции
		"EOF", "EOD",
	}
	if len(gotFile) != len(want) {
		t.Fatalf("filetape: ожидалось %v, получено %v", want, gotFile)
	}
	for i := range want {
		if gotFile[i] != want[i] {
			t.Fatalf("filetape: событие %d = %q, хочу %q (весь проход: %v)", i, gotFile[i], want[i], gotFile)
		}
	}
	if len(gotFake) != len(gotFile) {
		t.Fatalf("длины различаются: filetape=%v fake=%v", gotFile, gotFake)
	}
	for i := range gotFile {
		if gotFile[i] != gotFake[i] {
			t.Fatalf("событие %d: filetape=%q fake=%q\nfiletape: %v\nfake:     %v",
				i, gotFile[i], gotFake[i], gotFile, gotFake)
		}
	}
}
