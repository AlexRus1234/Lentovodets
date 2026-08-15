package testutil_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"lentovodec/internal/testutil"
)

func TestFakeTape_WriteReadSegments(t *testing.T) {
	ctx := context.Background()
	tape := testutil.NewFakeTape()

	for _, b := range []string{"one", "two"} {
		if err := tape.WriteBlock(ctx, []byte(b)); err != nil {
			t.Fatalf("WriteBlock(%q): %v", b, err)
		}
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	if err := tape.WriteBlock(ctx, []byte("three")); err != nil {
		t.Fatalf("WriteBlock(three): %v", err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	if got := tape.BlockCount(); got != 3 {
		t.Errorf("BlockCount = %d, want 3", got)
	}
	if got := tape.MarkCount(); got != 2 {
		t.Errorf("MarkCount = %d, want 2", got)
	}
	blocks, marks := tape.Snapshot()
	if len(blocks) != 3 || len(marks) != 2 {
		t.Fatalf("Snapshot: %d блоков, %d меток; want 3, 2", len(blocks), len(marks))
	}
	if marks[0] != 2 || marks[1] != 3 {
		t.Errorf("marks = %v, want [2 3]", marks)
	}

	if err := tape.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	want := []string{"one", "two", "", "three", "", ""}
	for i, w := range want {
		got, err := tape.ReadBlock(ctx)
		if w == "" {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("чтение %d: err = %v, want io.EOF", i, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("чтение %d: %v", i, err)
		}
		if string(got) != w {
			t.Errorf("чтение %d = %q, want %q", i, got, w)
		}
	}
}

func TestFakeTape_ForwardBackwardFilemarks(t *testing.T) {
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	// Раскладка: [b1][M][b2][b3][M]
	_ = tape.WriteBlock(ctx, []byte("b1"))
	_ = tape.WriteEOF(ctx)
	_ = tape.WriteBlock(ctx, []byte("b2"))
	_ = tape.WriteBlock(ctx, []byte("b3"))
	_ = tape.WriteEOF(ctx)

	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	// MTFSF(1): в начале файла 2 → читаем b2, b3, EOF.
	if err := tape.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("ForwardFilemarks(1): %v", err)
	}
	for _, w := range []string{"b2", "b3"} {
		got, err := tape.ReadBlock(ctx)
		if err != nil || string(got) != w {
			t.Fatalf("чтение: %q, %v; want %q", got, err, w)
		}
	}
	if _, err := tape.ReadBlock(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("чтение за файлом 2: %v, want io.EOF", err)
	}

	// Дочитали до конца (обе метки потреблены) — назад через обе.
	if err := tape.BackwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("BackwardFilemarks(2): %v", err)
	}
	got, err := tape.ReadBlock(ctx)
	if err != nil || string(got) != "b2" {
		t.Fatalf("после MTBSFM(2): %q, %v; want b2", got, err)
	}

	// За пределы — ошибка.
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.ForwardFilemarks(ctx, 3); err == nil {
		t.Error("ForwardFilemarks(3) за EOD: want error")
	}
	if err := tape.BackwardFilemarks(ctx, 1); err == nil {
		t.Error("BackwardFilemarks(1) из начала: want error")
	}
}

func TestFakeTape_WriteTruncatesTail(t *testing.T) {
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	_ = tape.WriteBlock(ctx, []byte("old1"))
	_ = tape.WriteEOF(ctx)
	_ = tape.WriteBlock(ctx, []byte("old2"))
	_ = tape.WriteEOF(ctx)

	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteBlock(ctx, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got := tape.BlockCount(); got != 2 {
		t.Errorf("BlockCount после перезаписи = %d, want 2 (хвост затёрт)", got)
	}
	if got := tape.MarkCount(); got != 1 {
		t.Errorf("MarkCount после перезаписи = %d, want 1", got)
	}
}

func TestFakeTape_EndOfDataAppends(t *testing.T) {
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	_ = tape.WriteBlock(ctx, []byte("label"))
	_ = tape.WriteEOF(ctx)
	_ = tape.WriteEOF(ctx)

	if err := tape.EndOfData(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteBlock(ctx, []byte("session")); err != nil {
		t.Fatal(err)
	}
	if got := tape.BlockCount(); got != 2 {
		t.Errorf("BlockCount = %d, want 2", got)
	}
	_, marks := tape.Snapshot()
	if len(marks) != 2 || marks[0] != 1 || marks[1] != 1 {
		t.Errorf("marks = %v, want [1 1]", marks)
	}
}

func TestFakeTape_Eject(t *testing.T) {
	tape := testutil.NewFakeTape()
	if tape.Ejected() {
		t.Error("новая лента уже извлечена")
	}
	if err := tape.Eject(context.Background()); err != nil {
		t.Fatalf("Eject: %v", err)
	}
	if !tape.Ejected() {
		t.Error("Ejected() = false после Eject")
	}
}

func TestFakeTape_CanceledContext(t *testing.T) {
	newTape := func() *testutil.FakeTape {
		tape := testutil.NewFakeTape()
		ctx := context.Background()
		_ = tape.WriteBlock(ctx, []byte("x"))
		_ = tape.WriteEOF(ctx)
		_ = tape.WriteBlock(ctx, []byte("y"))
		_ = tape.WriteEOF(ctx)
		return tape
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	ops := map[string]func() error{
		"ReadBlock":         func() error { _, err := newTape().ReadBlock(canceled); return err },
		"WriteBlock":        func() error { return newTape().WriteBlock(canceled, []byte("z")) },
		"WriteEOF":          func() error { return newTape().WriteEOF(canceled) },
		"ForwardFilemarks":  func() error { return newTape().ForwardFilemarks(canceled, 1) },
		"BackwardFilemarks": func() error { return newTape().BackwardFilemarks(canceled, 1) },
		"Rewind":            func() error { return newTape().Rewind(canceled) },
		"EndOfData":         func() error { return newTape().EndOfData(canceled) },
		"Eject":             func() error { return newTape().Eject(canceled) },
	}
	for name, op := range ops {
		err := op()
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("%s с отменённым ctx: err = %v, want context canceled", name, err)
		}
	}
}
