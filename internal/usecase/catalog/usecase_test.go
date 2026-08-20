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

func newCatalogUC(cat port.Catalog, tape port.Tape, codec *testutil.FakeCodec) *catalog.UseCase {
	return catalog.New(cat, tape, codec, nil, testutil.NoopLogger(), nil)
}

// newChainCatalogUC — use case со сменщиком кассет цепочки.
func newChainCatalogUC(cat port.Catalog, tape port.Tape, codec *testutil.FakeCodec, ch *testutil.FuncChanger) *catalog.UseCase {
	return catalog.New(cat, tape, codec, nil, testutil.NoopLogger(), ch)
}

func seedCatalog(t *testing.T) (*testutil.MemCatalog, int64, int64) {
	t.Helper()
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	if err := cat.RegisterTape(ctx, "u1", "t-1", 0); err != nil {
		t.Fatal(err)
	}
	id1, err := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 1, Type: domain.SessionFull, Timestamp: 100})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 2, Type: domain.SessionInc, Timestamp: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SaveFiles(ctx, id1, []domain.FileMeta{
		{Path: "/etc/hosts", Hash: "h1", State: domain.StateAdded},
	}); err != nil {
		t.Fatal(err)
	}
	if err := cat.SaveFiles(ctx, id2, []domain.FileMeta{
		{Path: "/etc/hosts", Hash: "h2", State: domain.StateModified},
		{Path: "/var/x", Hash: "x", State: domain.StateAdded},
	}); err != nil {
		t.Fatal(err)
	}
	return cat, id1, id2
}

func TestCatalog_ListTapes(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	tapes, err := uc.ListTapes(context.Background())
	if err != nil {
		t.Fatalf("ListTapes: %v", err)
	}
	if len(tapes) != 1 || tapes[0].UUID != "u1" || tapes[0].Name != "t-1" {
		t.Fatalf("tapes: %+v", tapes)
	}
}

func TestCatalog_ListSessions(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	all, err := uc.ListSessions(context.Background(), "")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListSessions(all): %d, %v", len(all), err)
	}
	one, err := uc.ListSessions(context.Background(), "u1")
	if err != nil || len(one) != 2 {
		t.Fatalf("ListSessions(u1): %d, %v", len(one), err)
	}
	if _, err := uc.ListSessions(context.Background(), "ghost"); err != nil || len(one) != 2 {
		t.Fatalf("ListSessions(ghost): %v", err)
	}
}

func TestCatalog_GetFiles(t *testing.T) {
	cat, id1, _ := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	files, err := uc.GetFiles(context.Background(), id1)
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	if len(files) != 1 || files[0].Hash != "h1" {
		t.Fatalf("files: %+v", files)
	}
	if _, err := uc.GetFiles(context.Background(), 999); !errors.Is(err, &domain.SessionNotFoundError{SessionID: 999}) {
		t.Fatalf("GetFiles(999): %v", err)
	}
}

func TestCatalog_Search(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	found, err := uc.Search(context.Background(), "hosts")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(found) != 2 { // обе сессии
		t.Fatalf("found %d; want 2", len(found))
	}
	found, err = uc.Search(context.Background(), "nothing")
	if err != nil || len(found) != 0 {
		t.Fatalf("Search(nothing): %d, %v", len(found), err)
	}
}

func TestCatalog_DeleteSession(t *testing.T) {
	cat, _, id2 := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	if err := uc.DeleteSession(context.Background(), id2); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	sessions, _ := cat.ListSessions(context.Background(), "u1")
	if len(sessions) != 1 {
		t.Fatalf("осталось %d сессий; want 1", len(sessions))
	}
	if err := uc.DeleteSession(context.Background(), id2); !errors.Is(err, &domain.SessionNotFoundError{SessionID: id2}) {
		t.Fatalf("повторное удаление: %v", err)
	}
}

func TestCatalog_Prune(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	n, err := uc.Prune(context.Background(), 150)
	if err != nil || n != 1 {
		t.Fatalf("Prune(150) = %d, %v; want 1", n, err)
	}
	n, err = uc.Prune(context.Background(), 300)
	if err != nil || n != 1 {
		t.Fatalf("Prune(300) = %d, %v; want 1", n, err)
	}
}

// labeledTape — лента с ярлыком.
func labeledTape(t *testing.T, codec *testutil.FakeCodec) *testutil.FakeTape {
	t.Helper()
	tape := testutil.NewFakeTape()
	label := domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "t-1", UUID: "u1", FormattedAt: "2026-01-01T00:00:00Z",
	}
	block, err := codec.EncodeLabel(label)
	if err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteBlock(context.Background(), block); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteEOF(context.Background()); err != nil {
		t.Fatal(err)
	}
	return tape
}

func TestCatalog_TapeInfo(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	tape := labeledTape(t, codec)
	uc := newCatalogUC(cat, tape, codec)

	info, err := uc.TapeInfo(context.Background())
	if err != nil {
		t.Fatalf("TapeInfo: %v", err)
	}
	if info.Label.UUID != "u1" || info.Label.Name != "t-1" {
		t.Fatalf("info: %+v", info)
	}
}

func TestCatalog_Eject(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	tape := labeledTape(t, codec)
	uc := newCatalogUC(cat, tape, codec)

	if err := uc.Eject(context.Background()); err != nil {
		t.Fatalf("Eject: %v", err)
	}
	if !tape.Ejected() {
		t.Error("лента не извлечена")
	}
}

func TestCatalog_ReadTest(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	tape := labeledTape(t, codec)
	codec.Queue = [][]domain.FileMeta{
		{{Path: "/a", Hash: "h", State: domain.StateAdded, Size: 10}},
		{{Path: "/b", Hash: "h", State: domain.StateAdded, Size: 20}},
	}
	uc := newCatalogUC(cat, tape, codec)

	reports, err := uc.ReadTest(context.Background())
	if err != nil {
		t.Fatalf("ReadTest: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("отчётов %d; want 1", len(reports))
	}
	r := reports[0]
	if r.Name != "t-1" || r.Sessions != 2 || r.Files != 2 || r.Bytes != 30 {
		t.Fatalf("отчёт: %+v", r)
	}
}

func TestCatalog_ReadTestDamaged(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	tape := labeledTape(t, codec)
	codec.Queue = [][]domain.FileMeta{{{Path: "/a"}}}
	codec.ErrReadOnce = errors.New("tapeformat: файл /a повреждён: хеш не совпал")
	codec.ErrReadOn = 1
	uc := newCatalogUC(cat, tape, codec)

	if _, err := uc.ReadTest(context.Background()); err == nil {
		t.Fatal("повреждённая сессия должна давать ошибку")
	}
}

func TestCatalog_ReadTestContinuationStops(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	tape := labeledTape(t, codec)
	codec.Queue = [][]domain.FileMeta{{{Path: "/a"}}}
	// за сессией — указатель продолжения: остановка без ошибки
	codec.ContOnCall = 2
	codec.Cont = &domain.ContinuationError{
		JobRunID: "run", SessionNum: 2, Part: 2, NextTapeName: "media-014",
	}
	uc := newCatalogUC(cat, tape, codec)

	n, err := uc.ReadTest(context.Background())
	if err != nil {
		t.Fatalf("ReadTest: %v; want nil (продолжение — не ошибка)", err)
	}
	if len(n) != 1 || n[0].Sessions != 1 {
		t.Fatalf("проверено %+v; want 1 кассета, 1 сессия", n)
	}
}

func TestCatalog_TapeErrors(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	uc := newCatalogUC(cat, testutil.NewFakeTape(), codec) // пустая лента

	if _, err := uc.TapeInfo(context.Background()); !errors.Is(err, &domain.BlankTapeError{}) {
		t.Fatalf("TapeInfo на пустой ленте: %v; want BlankTapeError", err)
	}
	if _, err := uc.ReadTest(context.Background()); !errors.Is(err, &domain.BlankTapeError{}) {
		t.Fatalf("ReadTest на пустой ленте: %v; want BlankTapeError", err)
	}
}

func TestCatalog_CanceledContext(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	uc := newCatalogUC(cat, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := uc.ListTapes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListTapes: %v", err)
	}
}

// failCat — MemCatalog с инъекцией сбоев по методам.
type failCat struct {
	*testutil.MemCatalog
	listTapes    error
	listSess     error
	getFiles     error
	search       error
	deleteSess   error
	prune        error
	registerTape error
	createSess   error
	saveFiles    error
}

func (c *failCat) RegisterTape(ctx context.Context, uuid, name string, formattedAt int64) error {
	if c.registerTape != nil {
		return c.registerTape
	}
	return c.MemCatalog.RegisterTape(ctx, uuid, name, formattedAt)
}

func (c *failCat) CreateSession(ctx context.Context, sess domain.Session) (int64, error) {
	if c.createSess != nil {
		return 0, c.createSess
	}
	return c.MemCatalog.CreateSession(ctx, sess)
}

func (c *failCat) SaveFiles(ctx context.Context, sessionID int64, files []domain.FileMeta) error {
	if c.saveFiles != nil {
		return c.saveFiles
	}
	return c.MemCatalog.SaveFiles(ctx, sessionID, files)
}

func (c *failCat) ListTapes(ctx context.Context) ([]port.TapeRecord, error) {
	if c.listTapes != nil {
		return nil, c.listTapes
	}
	return c.MemCatalog.ListTapes(ctx)
}

func (c *failCat) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	if c.listSess != nil {
		return nil, c.listSess
	}
	return c.MemCatalog.ListSessions(ctx, tapeUUID)
}

func (c *failCat) GetFilesBySession(ctx context.Context, sessionID int64) ([]domain.FileMeta, error) {
	if c.getFiles != nil {
		return nil, c.getFiles
	}
	return c.MemCatalog.GetFilesBySession(ctx, sessionID)
}

func (c *failCat) SearchFiles(ctx context.Context, pattern string) ([]port.FileCopy, error) {
	if c.search != nil {
		return nil, c.search
	}
	return c.MemCatalog.SearchFiles(ctx, pattern)
}

func (c *failCat) DeleteSession(ctx context.Context, sessionID int64) error {
	if c.deleteSess != nil {
		return c.deleteSess
	}
	return c.MemCatalog.DeleteSession(ctx, sessionID)
}

func (c *failCat) PruneSessions(ctx context.Context, before int64) (int64, error) {
	if c.prune != nil {
		return 0, c.prune
	}
	return c.MemCatalog.PruneSessions(ctx, before)
}

func TestCatalog_AdapterErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		cat  *failCat
		call func(uc *catalog.UseCase) error
	}{
		{
			name: "list tapes",
			cat:  &failCat{listTapes: boom},
			call: func(uc *catalog.UseCase) error {
				_, err := uc.ListTapes(context.Background())
				return err
			},
		},
		{
			name: "list sessions",
			cat:  &failCat{listSess: boom},
			call: func(uc *catalog.UseCase) error {
				_, err := uc.ListSessions(context.Background(), "")
				return err
			},
		},
		{
			name: "get files",
			cat:  &failCat{getFiles: boom},
			call: func(uc *catalog.UseCase) error {
				_, err := uc.GetFiles(context.Background(), 1)
				return err
			},
		},
		{
			name: "search",
			cat:  &failCat{search: boom},
			call: func(uc *catalog.UseCase) error {
				_, err := uc.Search(context.Background(), "x")
				return err
			},
		},
		{
			name: "delete session",
			cat:  &failCat{deleteSess: boom},
			call: func(uc *catalog.UseCase) error {
				return uc.DeleteSession(context.Background(), 1)
			},
		},
		{
			name: "prune",
			cat:  &failCat{prune: boom},
			call: func(uc *catalog.UseCase) error {
				_, err := uc.Prune(context.Background(), 1)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, id1, _ := seedCatalog(t)
			tc.cat.MemCatalog = base
			_ = id1
			uc := newCatalogUC(tc.cat, nil, nil)
			if err := tc.call(uc); !errors.Is(err, boom) {
				t.Fatalf("ожидался boom, got %v", err)
			}
		})
	}
}

// failTape — FakeTape с инъекцией сбоев.
type failTape struct {
	*testutil.FakeTape
	rewindErr error
	ejectErr  error
}

func (t *failTape) Rewind(ctx context.Context) error {
	if t.rewindErr != nil {
		return t.rewindErr
	}
	return t.FakeTape.Rewind(ctx)
}

func (t *failTape) Eject(ctx context.Context) error {
	if t.ejectErr != nil {
		return t.ejectErr
	}
	return t.FakeTape.Eject(ctx)
}

func TestCatalog_TapeOperationErrors(t *testing.T) {
	cat, _, _ := seedCatalog(t)
	boom := errors.New("boom")

	t.Run("tape info rewind fails", func(t *testing.T) {
		uc := newCatalogUC(cat, &failTape{rewindErr: boom}, &testutil.FakeCodec{})
		if _, err := uc.TapeInfo(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("TapeInfo: %v", err)
		}
	})
	t.Run("eject fails", func(t *testing.T) {
		uc := newCatalogUC(cat, &failTape{ejectErr: boom}, &testutil.FakeCodec{})
		if err := uc.Eject(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("Eject: %v", err)
		}
	})
	t.Run("readtest rewind fails", func(t *testing.T) {
		uc := newCatalogUC(cat, &failTape{rewindErr: boom}, &testutil.FakeCodec{})
		if _, err := uc.ReadTest(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("ReadTest: %v", err)
		}
	})
	t.Run("readtest transport error", func(t *testing.T) {
		codec := &testutil.FakeCodec{ErrRead: boom}
		tape := labeledTape(t, codec)
		uc := newCatalogUC(cat, tape, codec)
		if _, err := uc.ReadTest(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("ReadTest: %v", err)
		}
	})
	t.Run("readtest canceled mid-flight", func(t *testing.T) {
		codec := &testutil.FakeCodec{}
		tape := labeledTape(t, codec)
		// лента, игнорирующая ctx в Rewind/ReadBlock: отмена сработает
		// в цикле ReadTest
		uc := catalog.New(cat, &ctxFreeTape{tape}, codec, nil, testutil.NoopLogger(), nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := uc.ReadTest(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadTest: %v", err)
		}
	})
	t.Run("tape info on foreign tape", func(t *testing.T) {
		foreign := testutil.NewFakeTape()
		if err := foreign.WriteBlock(context.Background(), []byte("not ours")); err != nil {
			t.Fatal(err)
		}
		uc := newCatalogUC(cat, foreign, &testutil.FakeCodec{})
		if _, err := uc.TapeInfo(context.Background()); !errors.Is(err, &domain.ForeignFormatError{}) {
			t.Fatalf("TapeInfo: %v; want ForeignFormatError", err)
		}
	})
	t.Run("readtest on foreign tape", func(t *testing.T) {
		foreign := testutil.NewFakeTape()
		if err := foreign.WriteBlock(context.Background(), []byte("not ours")); err != nil {
			t.Fatal(err)
		}
		uc := newCatalogUC(cat, foreign, &testutil.FakeCodec{})
		if _, err := uc.ReadTest(context.Background()); !errors.Is(err, &domain.ForeignFormatError{}) {
			t.Fatalf("ReadTest: %v; want ForeignFormatError", err)
		}
	})
	t.Run("tape info read block fails", func(t *testing.T) {
		codec := &testutil.FakeCodec{}
		uc := newCatalogUC(cat, &readErrTape{FakeTape: testutil.NewFakeTape(), err: boom}, codec)
		if _, err := uc.TapeInfo(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("TapeInfo: %v", err)
		}
	})
	t.Run("readtest reports progress", func(t *testing.T) {
		codec := &testutil.FakeCodec{}
		tape := labeledTape(t, codec)
		codec.Queue = [][]domain.FileMeta{{{Path: "/a"}}}
		prog := &progRec{}
		uc := catalog.New(cat, tape, codec, prog, testutil.NoopLogger(), nil)
		if _, err := uc.ReadTest(context.Background()); err != nil {
			t.Fatalf("ReadTest: %v", err)
		}
		if prog.done != 1 {
			t.Errorf("done = %d; want 1", prog.done)
		}
	})
}

// ctxFreeTape — лента, чьи Rewind/ReadBlock игнорируют ctx.
type ctxFreeTape struct {
	*testutil.FakeTape
}

func (t *ctxFreeTape) Rewind(context.Context) error {
	return t.FakeTape.Rewind(context.Background())
}

func (t *ctxFreeTape) ReadBlock(context.Context) ([]byte, error) {
	return t.FakeTape.ReadBlock(context.Background())
}

// readErrTape — лента, чей ReadBlock всегда падает.
type readErrTape struct {
	*testutil.FakeTape
	err error
}

func (t *readErrTape) ReadBlock(context.Context) ([]byte, error) { return nil, t.err }

// progRec — запись событий прогресса.
type progRec struct{ done int }

func (p *progRec) Update(port.ProgressUpdate) {}
func (p *progRec) Done()                      { p.done++ }
func (p *progRec) Fail(error)                 {}
