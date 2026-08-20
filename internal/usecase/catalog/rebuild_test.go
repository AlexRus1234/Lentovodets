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

package catalog_test

import (
	"context"
	"errors"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/catalog"
)

// tapeWithMarks — лента с ярлыком и файлметками под sessions сессий
// (по две на сессию, FORMAT §4); FakeCodec содержимое сегментов не
// пишет, rebuild ходит только по меткам.
func tapeWithMarks(t *testing.T, codec *testutil.FakeCodec, sessions int) *testutil.FakeTape {
	t.Helper()
	tp := labeledTape(t, codec)
	ctx := context.Background()
	for i := 0; i < 2*sessions; i++ {
		if err := tp.WriteEOF(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return tp
}

// idxSession — пара заголовок+файлы для очереди FakeCodec.IdxQueue.
func idxSession(num int32, typ domain.SessionType, ts int64, run string, part int32, files ...domain.FileMeta) testutil.IdxSession {
	return testutil.IdxSession{
		Header: port.SessionHeader{
			SessionNum: num, Type: typ, Timestamp: ts,
			JobRunID: run, JobName: "daily", Part: part,
		},
		Files: files,
	}
}

func TestCatalog_Rebuild(t *testing.T) {
	codec := &testutil.FakeCodec{}
	codec.IdxQueue = []testutil.IdxSession{
		idxSession(1, domain.SessionFull, 100, "run-1", 1,
			domain.FileMeta{Path: "/data/a", Hash: "ha", State: domain.StateAdded, Size: 10},
			domain.FileMeta{Path: "/data/b", Hash: "hb", State: domain.StateModified, Size: 20}),
		idxSession(2, domain.SessionInc, 200, "run-2", 1,
			domain.FileMeta{Path: "/data/c", Hash: "hc", State: domain.StateAdded, Size: 30}),
	}
	tape := tapeWithMarks(t, codec, 2)
	cat := testutil.NewMemCatalog()
	uc := newCatalogUC(cat, tape, codec)

	rep, err := uc.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rep.Tapes != 1 || rep.TapeName != "t-1" || rep.Sessions != 2 ||
		rep.Files != 3 || rep.SkippedSessions != 0 || rep.NextTapeName != "" {
		t.Fatalf("отчёт: %+v", rep)
	}

	// кассета зарегистрирована, formatted_at перенесён из ярлыка
	rec, err := cat.GetTapeByUUID(context.Background(), "u1")
	if err != nil {
		t.Fatalf("GetTapeByUUID: %v", err)
	}
	if rec.Name != "t-1" || rec.FormattedAt != 1767225600 { // 2026-01-01T00:00:00Z
		t.Fatalf("кассета: %+v", rec)
	}

	sessions, err := cat.ListSessions(context.Background(), "u1")
	if err != nil || len(sessions) != 2 {
		t.Fatalf("сессии: %v %v", sessions, err)
	}
	want := []domain.Session{
		{TapeUUID: "u1", Num: 1, Type: domain.SessionFull, Timestamp: 100, JobRunID: "run-1", Part: 1},
		{TapeUUID: "u1", Num: 2, Type: domain.SessionInc, Timestamp: 200, JobRunID: "run-2", Part: 1},
	}
	for i, sess := range sessions {
		sess.ID = 0
		if sess != want[i] {
			t.Errorf("сессия[%d] = %+v; want %+v", i, sess, want[i])
		}
	}
	files, err := cat.GetFilesBySession(context.Background(), sessions[0].ID)
	if err != nil || len(files) != 2 || files[0].Path != "/data/a" || files[1].Path != "/data/b" {
		t.Fatalf("файлы сессии 1: %v %v", files, err)
	}
}

func TestCatalog_RebuildIdempotent(t *testing.T) {
	codec := &testutil.FakeCodec{}
	codec.IdxQueue = []testutil.IdxSession{
		idxSession(1, domain.SessionFull, 100, "run-1", 1,
			domain.FileMeta{Path: "/data/a", State: domain.StateAdded}),
		idxSession(2, domain.SessionInc, 200, "run-2", 1,
			domain.FileMeta{Path: "/data/b", State: domain.StateAdded}),
	}
	tape := tapeWithMarks(t, codec, 2)
	cat := testutil.NewMemCatalog()
	uc := newCatalogUC(cat, tape, codec)

	if _, err := uc.Rebuild(context.Background()); err != nil {
		t.Fatalf("первый Rebuild: %v", err)
	}
	// индексы снова «на ленте» (очередь фейка расходуется), каталог жив
	codec.IdxQueue = []testutil.IdxSession{
		idxSession(1, domain.SessionFull, 100, "run-1", 1,
			domain.FileMeta{Path: "/data/a", State: domain.StateAdded}),
		idxSession(2, domain.SessionInc, 200, "run-2", 1,
			domain.FileMeta{Path: "/data/b", State: domain.StateAdded}),
	}
	rep, err := uc.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("повторный Rebuild: %v", err)
	}
	if rep.Sessions != 0 || rep.SkippedSessions != 2 || rep.Files != 0 {
		t.Fatalf("повторный отчёт: %+v; хочу sessions=0 skipped=2 files=0", rep)
	}
	sessions, _ := cat.ListSessions(context.Background(), "u1")
	if len(sessions) != 2 {
		t.Fatalf("после повтора сессий %d; want 2 (без дублей)", len(sessions))
	}
}

func TestCatalog_RebuildSkipsExistingInLiveCatalog(t *testing.T) {
	codec := &testutil.FakeCodec{}
	codec.IdxQueue = []testutil.IdxSession{
		idxSession(1, domain.SessionFull, 100, "run-1", 1,
			domain.FileMeta{Path: "/data/a", State: domain.StateAdded}),
		idxSession(2, domain.SessionInc, 200, "run-2", 1,
			domain.FileMeta{Path: "/data/b", State: domain.StateAdded}),
	}
	tape := tapeWithMarks(t, codec, 2)
	cat := testutil.NewMemCatalog()
	ctx := context.Background()
	if err := cat.RegisterTape(ctx, "u1", "t-1", 0); err != nil {
		t.Fatal(err)
	}
	id, err := cat.CreateSession(ctx, domain.Session{
		TapeUUID: "u1", Num: 1, Type: domain.SessionFull, Timestamp: 100, JobRunID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	uc := newCatalogUC(cat, tape, codec)

	rep, err := uc.Rebuild(ctx)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rep.Sessions != 1 || rep.SkippedSessions != 1 {
		t.Fatalf("отчёт: %+v; хочу sessions=1 skipped=1", rep)
	}
	files, err := cat.GetFilesBySession(ctx, id)
	if err != nil || len(files) != 0 {
		t.Fatalf("существующая сессия перезаписана: %v %v", files, err)
	}
}

func TestCatalog_RebuildContinuationWarns(t *testing.T) {
	codec := &testutil.FakeCodec{}
	codec.IdxQueue = []testutil.IdxSession{
		idxSession(2, domain.SessionInc, 200, "run-2", 1,
			domain.FileMeta{Path: "/data/a", State: domain.StateAdded}),
	}
	codec.IdxCont = &domain.ContinuationError{
		JobRunID: "run-2", SessionNum: 2, Part: 2, NextTapeName: "media-014",
	}
	codec.IdxContOnCall = 2
	tape := tapeWithMarks(t, codec, 1)
	cat := testutil.NewMemCatalog()
	uc := newCatalogUC(cat, tape, codec)

	rep, err := uc.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("Rebuild: %v; want nil (продолжение — не ошибка)", err)
	}
	if rep.Sessions != 1 || rep.NextTapeName != "media-014" {
		t.Fatalf("отчёт: %+v; хочу sessions=1 next=media-014", rep)
	}
	// данные прочитанной части уже в каталоге
	sessions, _ := cat.ListSessions(context.Background(), "u1")
	if len(sessions) != 1 {
		t.Fatalf("сессий %d; want 1", len(sessions))
	}
}

func TestCatalog_RebuildTapeErrors(t *testing.T) {
	boom := errors.New("boom")
	t.Run("blank tape", func(t *testing.T) {
		uc := newCatalogUC(testutil.NewMemCatalog(), testutil.NewFakeTape(), &testutil.FakeCodec{})
		if _, err := uc.Rebuild(context.Background()); !errors.Is(err, &domain.BlankTapeError{}) {
			t.Fatalf("Rebuild на пустой ленте: %v; want BlankTapeError", err)
		}
	})
	t.Run("foreign tape", func(t *testing.T) {
		foreign := testutil.NewFakeTape()
		if err := foreign.WriteBlock(context.Background(), []byte("not ours")); err != nil {
			t.Fatal(err)
		}
		uc := newCatalogUC(testutil.NewMemCatalog(), foreign, &testutil.FakeCodec{})
		if _, err := uc.Rebuild(context.Background()); !errors.Is(err, &domain.ForeignFormatError{}) {
			t.Fatalf("Rebuild на чужой ленте: %v; want ForeignFormatError", err)
		}
	})
	t.Run("rewind fails", func(t *testing.T) {
		uc := newCatalogUC(testutil.NewMemCatalog(), &failTape{rewindErr: boom}, &testutil.FakeCodec{})
		if _, err := uc.Rebuild(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("Rebuild: %v; want boom", err)
		}
	})
	t.Run("label skip fails", func(t *testing.T) {
		codec := &testutil.FakeCodec{}
		// лента с ярлыком, но без filemark после него: MTFSF(1) невозможен
		bare := testutil.NewFakeTape()
		block, err := codec.EncodeLabel(domain.TapeLabel{
			Magic: domain.Magic, FormatVersion: domain.FormatVersion,
			Name: "t-1", UUID: "u1", FormattedAt: "2026-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := bare.WriteBlock(context.Background(), block); err != nil {
			t.Fatal(err)
		}
		if _, err := newCatalogUC(testutil.NewMemCatalog(), bare, codec).Rebuild(context.Background()); err == nil {
			t.Fatal("Rebuild без filemark после ярлыка = nil")
		}
	})
	t.Run("malformed formatted_at", func(t *testing.T) {
		codec := &testutil.FakeCodec{}
		tp := testutil.NewFakeTape()
		block, err := codec.EncodeLabel(domain.TapeLabel{
			Magic: domain.Magic, FormatVersion: domain.FormatVersion,
			Name: "t-1", UUID: "u1", FormattedAt: "not-a-time",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := tp.WriteBlock(context.Background(), block); err != nil {
			t.Fatal(err)
		}
		if err := tp.WriteEOF(context.Background()); err != nil {
			t.Fatal(err)
		}
		uc := newCatalogUC(testutil.NewMemCatalog(), tp, codec)
		if _, err := uc.Rebuild(context.Background()); err == nil || errors.Is(err, &domain.BlankTapeError{}) {
			t.Fatalf("Rebuild с битым formatted_at: %v; want ошибка разбора", err)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		codec := &testutil.FakeCodec{}
		uc := newCatalogUC(testutil.NewMemCatalog(), labeledTape(t, codec), codec)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := uc.Rebuild(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Rebuild: %v; want context.Canceled", err)
		}
	})
}

func TestCatalog_RebuildOperationErrors(t *testing.T) {
	boom := errors.New("boom")
	// freshCodec — кодек с одним индексом сессии на ленте (очередь
	// фейка расходуется, каждому кейсу — своя).
	freshCodec := func() *testutil.FakeCodec {
		c := &testutil.FakeCodec{}
		c.IdxQueue = []testutil.IdxSession{
			idxSession(1, domain.SessionFull, 100, "run-1", 1,
				domain.FileMeta{Path: "/data/a", State: domain.StateAdded}),
		}
		return c
	}

	t.Run("codec fails", func(t *testing.T) {
		c := &testutil.FakeCodec{ErrReadIdx: boom}
		tp := tapeWithMarks(t, c, 1)
		uc := newCatalogUC(testutil.NewMemCatalog(), tp, c)
		if _, err := uc.Rebuild(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("Rebuild: %v; want boom", err)
		}
	})
	t.Run("tar skip fails", func(t *testing.T) {
		c := freshCodec()
		// меток хватает только на ярлык и индекс: ForwardFilemarks(1)
		// после сессии 1 падает
		uc := newCatalogUC(testutil.NewMemCatalog(), labeledTape(t, c), c)
		if _, err := uc.Rebuild(context.Background()); err == nil {
			t.Fatal("Rebuild без метки tar = nil")
		}
	})
	t.Run("catalog fails", func(t *testing.T) {
		cases := []struct {
			name string
			cat  *failCat
		}{
			{name: "register tape", cat: &failCat{registerTape: boom}},
			{name: "list sessions", cat: &failCat{listSess: boom}},
			{name: "create session", cat: &failCat{createSess: boom}},
			{name: "save files", cat: &failCat{saveFiles: boom}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				tc.cat.MemCatalog = testutil.NewMemCatalog()
				c := freshCodec()
				uc := newCatalogUC(tc.cat, tapeWithMarks(t, c, 1), c)
				if _, err := uc.Rebuild(context.Background()); !errors.Is(err, boom) {
					t.Fatalf("Rebuild: %v; want boom", err)
				}
			})
		}
	})
	t.Run("progress done is not expected", func(t *testing.T) {
		// rebuild не ведёт прогресс: быстрый проход по индексам
		c := freshCodec()
		prog := &progRec{}
		uc := catalog.New(testutil.NewMemCatalog(), tapeWithMarks(t, c, 1), c,
			prog, testutil.NoopLogger(), nil)
		rep, err := uc.Rebuild(context.Background())
		if err != nil {
			t.Fatalf("Rebuild: %v", err)
		}
		if rep.Sessions != 1 || prog.done != 0 {
			t.Fatalf("rep=%+v done=%d; want sessions=1 done=0", rep, prog.done)
		}
	})
}
