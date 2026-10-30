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

package inspect_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/inspect"
)

// newTape — фейк-лента с ярлыком и файлметками под sessions сессий
// (по две на сессию, FORMAT §4): FakeCodec содержимое сегментов не
// пишет, inspect ходит только по меткам (как tapeWithMarks у rebuild).
func newTape(t *testing.T, codec *testutil.FakeCodec, sessions int) *testutil.FakeTape {
	t.Helper()
	tp := testutil.NewFakeTape()
	block, err := codec.EncodeLabel(domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "T1", UUID: "11111111-1111-4111-8111-111111111111",
		FormattedAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := tp.WriteBlock(ctx, block); err != nil {
		t.Fatal(err)
	}
	if err := tp.WriteEOF(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*sessions; i++ {
		if err := tp.WriteEOF(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return tp
}

func TestInspect_LabelAndSessions(t *testing.T) {
	codec := &testutil.FakeCodec{}
	tp := newTape(t, codec, 2)
	codec.IdxQueue = []testutil.IdxSession{
		{
			Header: port.SessionHeader{
				SessionNum: 1, Type: domain.SessionFull, Timestamp: 100,
				JobRunID: "run-1", JobName: "media", Part: 1,
			},
			Files: []domain.FileMeta{
				{Path: "/data/a", Size: 10, State: domain.StateAdded},
				{Path: "/data/dir", Size: 0, IsDir: true},
			},
		},
		{
			Header: port.SessionHeader{
				SessionNum: 2, Type: domain.SessionInc, Timestamp: 200,
				JobRunID: "run-2", JobName: "media", Part: 1,
			},
			Files: []domain.FileMeta{
				{Path: "/data/b", Size: 5, State: domain.StateModified},
				{Path: "/data/gone", Size: 0, State: domain.StateDeleted},
			},
		},
	}
	uc := inspect.New(tp, codec, testutil.NoopLogger())

	rep, err := uc.Inspect(context.Background())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if rep.Label.Name != "T1" || rep.Label.UUID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("ярлык: %+v", rep.Label)
	}
	if len(rep.Sessions) != 2 {
		t.Fatalf("сессий: %d, want 2", len(rep.Sessions))
	}
	// Каталоги и tombstone'ы не имеют контента: 10 и 5 байт.
	if rep.Sessions[0].Bytes != 10 || rep.Sessions[1].Bytes != 5 {
		t.Fatalf("байты: %d, %d; want 10, 5", rep.Sessions[0].Bytes, rep.Sessions[1].Bytes)
	}
	if rep.NextTapeName != "" {
		t.Fatalf("next_tape: %q, want пусто", rep.NextTapeName)
	}
}

func TestInspect_Continuation(t *testing.T) {
	codec := &testutil.FakeCodec{}
	tp := newTape(t, codec, 2)
	codec.IdxQueue = []testutil.IdxSession{
		{
			Header: port.SessionHeader{SessionNum: 1, Type: domain.SessionFull, Part: 2},
			Files:  []domain.FileMeta{{Path: "/data/a", Size: 1, State: domain.StateAdded}},
		},
	}
	codec.IdxCont = &domain.ContinuationError{
		JobRunID: "run-1", SessionNum: 1, Part: 2, NextTapeName: "media-014",
	}
	codec.IdxContOnCall = 2
	uc := inspect.New(tp, codec, testutil.NoopLogger())

	rep, err := uc.Inspect(context.Background())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(rep.Sessions) != 1 {
		t.Fatalf("сессий: %d, want 1", len(rep.Sessions))
	}
	if rep.NextTapeName != "media-014" {
		t.Fatalf("next_tape: %q, want media-014", rep.NextTapeName)
	}
}

func TestInspect_DamagedIndexIsError(t *testing.T) {
	codec := &testutil.FakeCodec{}
	tp := newTape(t, codec, 2)
	codec.IdxQueue = []testutil.IdxSession{
		{Header: port.SessionHeader{SessionNum: 1, Type: domain.SessionFull, Part: 1}},
	}
	codec.ErrReadIdx = errors.Join(&domain.SessionDamageError{},
		&domain.IndexCorruptError{SessionNum: 2})
	uc := inspect.New(tp, codec, testutil.NoopLogger())

	_, err := uc.Inspect(context.Background())
	if err == nil {
		t.Fatal("повреждённый индекс = nil ошибка")
	}
	// Ошибка относится ко второй сессии и несёт тип порчи.
	if !strings.Contains(err.Error(), "сессия 2") {
		t.Fatalf("ошибка без номера сессии: %v", err)
	}
	var corrupt *domain.IndexCorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("ошибка не IndexCorruptError: %v", err)
	}
}

func TestInspect_BlankTape(t *testing.T) {
	codec := &testutil.FakeCodec{}
	uc := inspect.New(testutil.NewFakeTape(), codec, testutil.NoopLogger())

	_, err := uc.Inspect(context.Background())
	if err == nil {
		t.Fatal("пустая лента = nil ошибка")
	}
	var blank *domain.BlankTapeError
	if !errors.As(err, &blank) {
		t.Fatalf("ошибка не BlankTapeError: %v", err)
	}
}

func TestInspect_CtxCanceled(t *testing.T) {
	codec := &testutil.FakeCodec{}
	tp := newTape(t, codec, 2)
	uc := inspect.New(tp, codec, testutil.NoopLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := uc.Inspect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка отмены: %v", err)
	}
}
