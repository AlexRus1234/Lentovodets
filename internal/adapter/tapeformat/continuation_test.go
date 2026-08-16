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

package tapeformat_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// contFixture — детерминированный указатель продолжения.
func contFixture() port.Continuation {
	return port.Continuation{
		JobRunID:     "0f4c2d6a-1111-4222-8333-444455556666",
		SessionNum:   7,
		Part:         2,
		NextTapeName: "media-014",
	}
}

func TestWriteContinuation_ReadRoundTrip(t *testing.T) {
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	want := contFixture()
	if err := tapeformat.WriteContinuation(ctx, tape, want); err != nil {
		t.Fatalf("WriteContinuation: %v", err)
	}

	blocks, marks := tape.Snapshot()
	if len(blocks) != 1 || len(blocks[0]) != domain.BlockSize {
		t.Fatalf("указатель: %d блоков, первый %d байт; want 1 блок %d байт",
			len(blocks), len(blocks[0]), domain.BlockSize)
	}
	if len(marks) != 0 {
		t.Fatalf("WriteContinuation ставит filemark'и %+v; want нет (пара — зона вызывающего)", marks)
	}

	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := tapeformat.ReadContinuation(ctx, tape)
	if err != nil {
		t.Fatalf("ReadContinuation: %v", err)
	}
	if got != want {
		t.Fatalf("roundtrip: %+v; want %+v", got, want)
	}
}

func TestReadContinuation_EOF(t *testing.T) {
	ctx := context.Background()
	empty := testutil.NewFakeTape()
	if _, err := tapeformat.ReadContinuation(ctx, empty); !errors.Is(err, io.EOF) {
		t.Fatalf("пустая лента: %v; want io.EOF", err)
	}

	blank := testutil.NewFakeTape()
	if err := blank.WriteBlock(ctx, make([]byte, domain.BlockSize)); err != nil {
		t.Fatal(err)
	}
	if err := blank.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tapeformat.ReadContinuation(ctx, blank); !errors.Is(err, io.EOF) {
		t.Fatalf("нулевой блок: %v; want io.EOF", err)
	}
}

func TestReadContinuation_NotContinuation(t *testing.T) {
	ctx := context.Background()
	cases := map[string][]byte{
		"битый JSON":   []byte("\x01\x02 не json"),
		"чужой JSON":   []byte(`{"format_version":2,"session_num":1}`),
		"чужой kind":   []byte(`{"kind":"index"}`),
		"просто текст": []byte("GNU tar padding"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			tape := testutil.NewFakeTape()
			padded := make([]byte, domain.BlockSize)
			copy(padded, raw)
			if err := tape.WriteBlock(ctx, padded); err != nil {
				t.Fatal(err)
			}
			if err := tape.Rewind(ctx); err != nil {
				t.Fatal(err)
			}
			_, err := tapeformat.ReadContinuation(ctx, tape)
			var not *domain.NotContinuationError
			if !errors.As(err, &not) {
				t.Fatalf("err = %v; want NotContinuationError", err)
			}
			if not.Snippet == "" {
				t.Error("Snippet пуст")
			}
		})
	}
}

func TestReadContinuation_TapeError(t *testing.T) {
	boom := errors.New("boom")
	tape := &hookTape{Tape: testutil.NewFakeTape(), onRead: func(int) error { return boom }}
	_, err := tapeformat.ReadContinuation(context.Background(), tape)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v; want обёрнутый boom", err)
	}
	if !strings.Contains(err.Error(), "чтение указателя продолжения") {
		t.Errorf("err = %v; want упоминание чтения указателя", err)
	}
}

func TestWriteContinuation_Errors(t *testing.T) {
	ctx := context.Background()
	t.Run("без имени кассеты", func(t *testing.T) {
		err := tapeformat.WriteContinuation(ctx, testutil.NewFakeTape(), port.Continuation{Part: 2})
		if err == nil || !strings.Contains(err.Error(), "имени следующей кассеты") {
			t.Fatalf("err = %v; want ошибка про имя кассеты", err)
		}
	})
	t.Run("JSON не влезает в блок", func(t *testing.T) {
		c := contFixture()
		c.NextTapeName = strings.Repeat("m", domain.BlockSize)
		err := tapeformat.WriteContinuation(ctx, testutil.NewFakeTape(), c)
		if err == nil || !strings.Contains(err.Error(), "кодирование указателя продолжения") {
			t.Fatalf("err = %v; want ошибка кодирования", err)
		}
	})
	t.Run("сбой записи блока", func(t *testing.T) {
		boom := errors.New("boom")
		tape := &hookTape{Tape: testutil.NewFakeTape(), onWrite: func(int) error { return boom }}
		err := tapeformat.WriteContinuation(ctx, tape, contFixture())
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v; want обёрнутый boom", err)
		}
	})
}

func TestReadSession_ContinuationError(t *testing.T) {
	ctx := context.Background()
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs) // заканчивается filemark'ом tar
	want := contFixture()
	if err := tapeformat.WriteContinuation(ctx, tape, want); err != nil {
		t.Fatal(err)
	}
	// Закрывающая EOD-пара после блока — новый EOD кассеты.
	for i := 0; i < 2; i++ {
		if err := tape.WriteEOF(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tapeformat.ReadSession(ctx, tape, nil, nil); err != nil {
		t.Fatalf("первая сессия: %v", err)
	}
	_, err := tapeformat.ReadSession(ctx, tape, nil, nil)
	var cont *domain.ContinuationError
	if !errors.As(err, &cont) {
		t.Fatalf("err = %v; want ContinuationError", err)
	}
	if cont.NextTapeName != want.NextTapeName || cont.JobRunID != want.JobRunID ||
		cont.SessionNum != want.SessionNum || cont.Part != want.Part {
		t.Fatalf("continuation: %+v; want %+v", *cont, want)
	}
	if !errors.Is(err, &domain.ContinuationError{}) {
		t.Error("errors.Is(&ContinuationError{}) не сработал")
	}
}

func TestCodec_ContinuationRoundtrip(t *testing.T) {
	var codec port.TapeCodec = tapeformat.NewCodec()
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	want := contFixture()
	if err := codec.WriteContinuation(ctx, tape, want); err != nil {
		t.Fatalf("WriteContinuation: %v", err)
	}
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := codec.ReadContinuation(ctx, tape)
	if err != nil {
		t.Fatalf("ReadContinuation: %v", err)
	}
	if got != want {
		t.Fatalf("roundtrip: %+v; want %+v", got, want)
	}
}

func TestGoldenContinuation(t *testing.T) {
	tape := testutil.NewFakeTape()
	if err := tapeformat.WriteContinuation(context.Background(), tape, contFixture()); err != nil {
		t.Fatalf("WriteContinuation: %v", err)
	}
	blocks, _ := tape.Snapshot()
	checkGolden(t, "continuation.bin", blocks[0])
}
