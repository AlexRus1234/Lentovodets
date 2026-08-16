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

package restore_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/restore"
)

// harness — лента с ярлыком, каталог с кассетой и сессиями, очередь
// сессий кодека.
type harness struct {
	tape        *recTape
	codec       *testutil.FakeCodec
	cat         *testutil.MemCatalog
	dest        *testutil.MapFS
	uc          *restore.UseCase
	prog        *progRecorder
	selectiveID int64
}

// recTape запоминает аргументы ForwardFilemarks и инъектирует сбои.
type recTape struct {
	*testutil.FakeTape
	fsf       []int
	fsfErr    error
	rewindErr error
	readErr   error
}

func (t *recTape) ForwardFilemarks(ctx context.Context, n int) error {
	t.fsf = append(t.fsf, n)
	if t.fsfErr != nil {
		return t.fsfErr
	}
	return t.FakeTape.ForwardFilemarks(ctx, n)
}

func (t *recTape) Rewind(ctx context.Context) error {
	if t.rewindErr != nil {
		return t.rewindErr
	}
	return t.FakeTape.Rewind(ctx)
}

func (t *recTape) ReadBlock(ctx context.Context) ([]byte, error) {
	if t.readErr != nil {
		return nil, t.readErr
	}
	return t.FakeTape.ReadBlock(ctx)
}

type progRecorder struct {
	updates []port.ProgressUpdate
	done    int
	fails   int
}

func (p *progRecorder) Update(u port.ProgressUpdate) { p.updates = append(p.updates, u) }
func (p *progRecorder) Done()                        { p.done++ }
func (p *progRecorder) Fail(error)                   { p.fails++ }

func meta(path string) domain.FileMeta {
	return domain.FileMeta{Path: path, Size: 1, State: domain.StateAdded}
}

// addSession добавляет сессию num в каталог, очередь чтения кодека
// и дописывает на ленту 2 filemark'а (модель записанной сессии).
func (h *harness) addSession(t *testing.T, num int32, files []domain.FileMeta) int64 {
	t.Helper()
	id, err := h.cat.CreateSession(context.Background(), domain.Session{
		TapeUUID:  "tape-uuid",
		Num:       num,
		Type:      domain.SessionFull,
		Timestamp: int64(100 + num),
		JobRunID:  "run",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.cat.SaveFiles(context.Background(), id, files); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	h.codec.Queue = append(h.codec.Queue, files)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := h.tape.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF: %v", err)
		}
	}
	return id
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	codec := &testutil.FakeCodec{}
	tape := &recTape{FakeTape: testutil.NewFakeTape()}
	label := domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "t-1", UUID: "tape-uuid",
	}
	block, err := codec.EncodeLabel(label)
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	ctx := context.Background()
	if err := tape.WriteBlock(ctx, block); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := tape.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF: %v", err)
		}
	}
	cat := testutil.NewMemCatalog()
	if err := cat.RegisterTape(ctx, label.UUID, label.Name, 0); err != nil {
		t.Fatalf("RegisterTape: %v", err)
	}
	h := &harness{
		tape:  tape,
		codec: codec,
		cat:   cat,
		dest:  testutil.NewMapFS(nil),
		prog:  &progRecorder{},
	}
	h.uc = restore.New(tape, codec, cat, h.dest, h.prog, testutil.NoopLogger())
	return h
}

func TestRestore_Full(t *testing.T) {
	h := newHarness(t)
	id1 := h.addSession(t, 1, []domain.FileMeta{
		{Path: "/etc", IsDir: true, State: domain.StateAdded},
		meta("/etc/hosts"),
	})
	_ = id1
	h.addSession(t, 2, []domain.FileMeta{meta("/var/log/x")})

	st, err := h.uc.Full(context.Background())
	if err != nil {
		t.Fatalf("Full: %v", err)
	}
	if st.Sessions != 2 || st.Files != 2 || st.Dirs != 1 {
		t.Fatalf("статистика: %+v", st)
	}
	if h.codec.ReadCalls != 3 { // 2 сессии + EOD-проверка
		t.Errorf("ReadCalls = %d; want 3", h.codec.ReadCalls)
	}
	if h.prog.done != 1 || h.prog.fails != 0 {
		t.Errorf("прогресс: done=%d fails=%d", h.prog.done, h.prog.fails)
	}
}

func TestRestore_FullSkipsDamagedSession(t *testing.T) {
	h := newHarness(t)
	h.addSession(t, 1, []domain.FileMeta{meta("/a")})
	h.addSession(t, 3, []domain.FileMeta{meta("/c")})
	// вторая прочитанная сессия повреждена: пропуск и продолжение
	h.codec.ErrReadOnce = errors.New("tapeformat: файл /b повреждён: хеш индекса не совпал")
	h.codec.ErrReadOn = 2

	st, err := h.uc.Full(context.Background())
	if err != nil {
		t.Fatalf("Full: %v", err)
	}
	// 2 успешные + 1 пропущенная повреждённая
	if st.Sessions != 3 || st.Files != 2 {
		t.Fatalf("повреждённая сессия прервала restore: %+v", st)
	}
}

func TestRestore_FullTransportErrorAborts(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("driver failure")
	h.codec.ErrRead = boom
	if _, err := h.uc.Full(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Full: %v; want boom", err)
	}
	if h.prog.done != 0 {
		t.Errorf("done = %d; want 0", h.prog.done)
	}
}

func TestRestore_FullAppliesTombstones(t *testing.T) {
	h := newHarness(t)
	h.dest.MapFS["etc/old"] = &fstest.MapFile{Data: []byte("x")}
	h.addSession(t, 1, []domain.FileMeta{
		{Path: "/etc/old", State: domain.StateDeleted},
		meta("/etc/new"),
	})

	if _, err := h.uc.Full(context.Background()); err != nil {
		t.Fatalf("Full: %v", err)
	}
	if _, ok := h.dest.MapFS["etc/old"]; ok {
		t.Error("tombstone /etc/old не удалён из целевой ФС")
	}
}

func TestRestore_FullContinuationStops(t *testing.T) {
	h := newHarness(t)
	h.addSession(t, 1, []domain.FileMeta{meta("/a")})
	// вторая «сессия» — указатель продолжения: Warn и остановка без ошибки
	h.codec.ContOnCall = 2
	h.codec.Cont = &domain.ContinuationError{
		JobRunID: "run", SessionNum: 2, Part: 2, NextTapeName: "media-014",
	}

	st, err := h.uc.Full(context.Background())
	if err != nil {
		t.Fatalf("Full: %v; want nil (продолжение — не ошибка)", err)
	}
	if st.Sessions != 1 || st.Files != 1 {
		t.Fatalf("статистика: %+v; want 1 сессия, 1 файл", st)
	}
	if h.prog.done != 1 || h.prog.fails != 0 {
		t.Errorf("прогресс: done=%d fails=%d; want 1/0", h.prog.done, h.prog.fails)
	}
}

func TestRestore_Selective(t *testing.T) {
	h := newHarness(t)
	id := h.addSession(t, 1, []domain.FileMeta{
		{Path: "/etc", IsDir: true, State: domain.StateAdded},
		meta("/etc/hosts"),
		meta("/etc/fstab"),
		meta("/var/x"),
	})

	st, err := h.uc.Selective(context.Background(), id, []string{"/etc/hosts"})
	if err != nil {
		t.Fatalf("Selective: %v", err)
	}
	if st.Files != 1 {
		t.Fatalf("Files = %d; want 1", st.Files)
	}
	// MTFSF(2*1-1) = 1
	if len(h.tape.fsf) != 1 || h.tape.fsf[0] != 1 {
		t.Errorf("ForwardFilemarks args = %v; want [1]", h.tape.fsf)
	}
	if h.prog.done != 1 {
		t.Errorf("done = %d; want 1", h.prog.done)
	}
}

func TestRestore_SelectiveSessionNotFound(t *testing.T) {
	h := newHarness(t)
	_, err := h.uc.Selective(context.Background(), 999, nil)
	if !errors.Is(err, &domain.SessionNotFoundError{SessionID: 999}) {
		t.Fatalf("Selective: %v; want SessionNotFoundError", err)
	}
}

func TestRestore_SelectiveUnderRoot(t *testing.T) {
	h := newHarness(t)
	id := h.addSession(t, 1, []domain.FileMeta{
		{Path: "/etc", IsDir: true, State: domain.StateAdded},
		meta("/etc/hosts"),
		meta("/etc/fstab"),
		meta("/var/x"),
	})
	st, err := h.uc.Selective(context.Background(), id, []string{"/etc"})
	if err != nil {
		t.Fatalf("Selective: %v", err)
	}
	if st.Files != 2 {
		t.Fatalf("Files = %d; want 2 (весь каталог /etc)", st.Files)
	}
}

func TestRestore_SmartNewestCopy(t *testing.T) {
	h := newHarness(t)
	// сессия 1: старая копия; сессия 2: новая; обе в каталоге и на ленте
	h.addSession(t, 1, []domain.FileMeta{meta("/etc/hosts")})
	h.addSession(t, 2, []domain.FileMeta{meta("/etc/hosts")})

	st, err := h.uc.Smart(context.Background(), []string{"/etc/hosts"})
	if err != nil {
		t.Fatalf("Smart: %v", err)
	}
	if st.Files != 1 {
		t.Fatalf("Files = %d; want 1", st.Files)
	}
	if h.codec.ReadCalls != 1 {
		t.Errorf("ReadCalls = %d; want 1 (новейшая копия достаточна)", h.codec.ReadCalls)
	}
	// MTFSF(2*2-1) = 3 — сессия 2 (новейшая по timestamp каталога)
	if len(h.tape.fsf) != 1 || h.tape.fsf[0] != 3 {
		t.Errorf("ForwardFilemarks args = %v; want [3]", h.tape.fsf)
	}
	if h.prog.done != 1 {
		t.Errorf("done = %d; want 1", h.prog.done)
	}
}

func TestRestore_SmartFallsBackToOlderCopy(t *testing.T) {
	h := newHarness(t)
	h.addSession(t, 1, []domain.FileMeta{meta("/etc/hosts")})
	h.addSession(t, 2, []domain.FileMeta{meta("/etc/hosts")})
	// новейшая копия (первое чтение) повреждена — читается старая
	h.codec.ErrReadOnce = errors.New("tapeformat: файл /etc/hosts повреждён: хеш")
	h.codec.ErrReadOn = 1

	st, err := h.uc.Smart(context.Background(), []string{"/etc/hosts"})
	if err != nil {
		t.Fatalf("Smart: %v", err)
	}
	if st.Files != 1 {
		t.Fatalf("Files = %d; want 1 (fallback на старую копию)", st.Files)
	}
	if h.codec.ReadCalls != 2 {
		t.Errorf("ReadCalls = %d; want 2 (новейшая упала, старая прочитана)", h.codec.ReadCalls)
	}
	if h.prog.fails != 0 || h.prog.done != 1 {
		t.Errorf("прогресс: fails=%d done=%d; want 0/1", h.prog.fails, h.prog.done)
	}
}

func TestRestore_SmartNoHealthyCopy(t *testing.T) {
	h := newHarness(t)
	h.addSession(t, 1, []domain.FileMeta{meta("/gone")})
	h.codec.Queue = nil
	h.codec.ReadFiles = nil
	h.codec.ErrRead = errors.New("tapeformat: чтение индекса: i/o error")

	_, err := h.uc.Smart(context.Background(), []string{"/etc/hosts"})
	if !errors.Is(err, &domain.NoHealthyCopyError{}) {
		t.Fatalf("Smart: %v; want NoHealthyCopyError", err)
	}
	if h.prog.fails != 1 {
		t.Errorf("fails = %d; want 1", h.prog.fails)
	}
}

func TestRestore_SmartUnknownPath(t *testing.T) {
	h := newHarness(t)
	// путь нигде не записан: копий нет → NoHealthyCopy
	_, err := h.uc.Smart(context.Background(), []string{"/nowhere"})
	if !errors.Is(err, &domain.NoHealthyCopyError{}) {
		t.Fatalf("Smart: %v; want NoHealthyCopyError", err)
	}
}

func TestRestore_SmartEmptyPaths(t *testing.T) {
	h := newHarness(t)
	if _, err := h.uc.Smart(context.Background(), nil); err == nil {
		t.Fatal("пустой список путей должен давать ошибку")
	}
}

func TestRestore_BlankTape(t *testing.T) {
	h := newHarness(t)
	h.tape = &recTape{FakeTape: testutil.NewFakeTape()}
	h.uc = restore.New(h.tape, h.codec, h.cat, h.dest, nil, testutil.NoopLogger())
	if _, err := h.uc.Full(context.Background()); !errors.Is(err, &domain.BlankTapeError{}) {
		t.Fatalf("Full: %v; want BlankTapeError", err)
	}
}

func TestRestore_CanceledContext(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.uc.Full(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Full: %v; want context.Canceled", err)
	}
}

func TestRestore_ErrorPaths(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		prep func(t *testing.T, h *harness)
		call func(h *harness) error
	}{
		{
			name: "full rewind fails",
			prep: func(t *testing.T, h *harness) { h.tape.rewindErr = boom },
			call: func(h *harness) error {
				_, err := h.uc.Full(context.Background())
				return err
			},
		},
		{
			name: "full read block fails",
			prep: func(t *testing.T, h *harness) { h.tape.readErr = boom },
			call: func(h *harness) error {
				_, err := h.uc.Full(context.Background())
				return err
			},
		},
		{
			name: "selective list sessions fails",
			prep: func(t *testing.T, h *harness) {
				id := h.addSession(t, 1, []domain.FileMeta{meta("/a")})
				h.selectiveID = id
				h.uc = restore.New(h.tape, h.codec, &failCat{listSessions: boom}, h.dest, nil, testutil.NoopLogger())
			},
			call: func(h *harness) error {
				_, err := h.uc.Selective(context.Background(), h.selectiveID, nil)
				return err
			},
		},
		{
			name: "selective positioning fails",
			prep: func(t *testing.T, h *harness) {
				id := h.addSession(t, 1, []domain.FileMeta{meta("/a")})
				h.selectiveID = id
				h.tape.fsfErr = boom
			},
			call: func(h *harness) error {
				_, err := h.uc.Selective(context.Background(), h.selectiveID, nil)
				return err
			},
		},
		{
			name: "selective read fails",
			prep: func(t *testing.T, h *harness) {
				id := h.addSession(t, 1, []domain.FileMeta{meta("/a")})
				h.selectiveID = id
				h.codec.ErrRead = boom
			},
			call: func(h *harness) error {
				_, err := h.uc.Selective(context.Background(), h.selectiveID, nil)
				return err
			},
		},
		{
			name: "smart rewind fails",
			prep: func(t *testing.T, h *harness) { h.tape.rewindErr = boom },
			call: func(h *harness) error {
				_, err := h.uc.Smart(context.Background(), []string{"/a"})
				return err
			},
		},
		{
			name: "smart read block fails",
			prep: func(t *testing.T, h *harness) { h.tape.readErr = boom },
			call: func(h *harness) error {
				_, err := h.uc.Smart(context.Background(), []string{"/a"})
				return err
			},
		},
		{
			name: "smart copies query fails",
			prep: func(t *testing.T, h *harness) {
				h.uc = restore.New(h.tape, h.codec, &failCat{copies: boom}, h.dest, nil, testutil.NoopLogger())
			},
			call: func(h *harness) error {
				_, err := h.uc.Smart(context.Background(), []string{"/a"})
				return err
			},
		},
		{
			name: "smart positioning fails for all copies",
			prep: func(t *testing.T, h *harness) {
				h.addSession(t, 1, []domain.FileMeta{meta("/a")})
				h.tape.fsfErr = boom
			},
			call: func(h *harness) error {
				_, err := h.uc.Smart(context.Background(), []string{"/a"})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.prep(t, h)
			err := tc.call(h)
			if !errors.Is(err, boom) && !errors.Is(err, &domain.NoHealthyCopyError{}) {
				t.Fatalf("ожидалась ошибка boom/NoHealthyCopy, got %v", err)
			}
		})
	}
}

func TestRestore_FullForeignLabel(t *testing.T) {
	h := newHarness(t)
	foreign := &recTape{FakeTape: testutil.NewFakeTape()}
	if err := foreign.WriteBlock(context.Background(), []byte("foreign data")); err != nil {
		t.Fatal(err)
	}
	h.tape = foreign
	h.uc = restore.New(foreign, h.codec, h.cat, h.dest, nil, testutil.NoopLogger())
	if _, err := h.uc.Full(context.Background()); !errors.Is(err, &domain.ForeignFormatError{}) {
		t.Fatalf("Full: %v; want ForeignFormatError", err)
	}
}

func TestRestore_FullSkipHitsTapeEnd(t *testing.T) {
	// одна метка на ленте: повреждённая сессия + исчерпание меток
	codec := &testutil.FakeCodec{}
	codec.ErrReadOnce = errors.New("tapeformat: файл /x повреждён: хеш")
	codec.ErrReadOn = 1
	tape := &recTape{FakeTape: testutil.NewFakeTape()}
	label := domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "t", UUID: "tape-uuid",
	}
	block, err := codec.EncodeLabel(label)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := tape.WriteBlock(ctx, block); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteEOF(ctx); err != nil { // только одна метка
		t.Fatal(err)
	}
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, label.UUID, label.Name, 0)
	uc := restore.New(tape, codec, cat, testutil.NewMapFS(nil), nil, testutil.NoopLogger())

	st, err := uc.Full(ctx)
	if err != nil {
		t.Fatalf("Full: %v", err)
	}
	if st.Sessions != 1 { // повреждённая сессия посчитана и пропущена
		t.Fatalf("Sessions = %d; want 1", st.Sessions)
	}
}

func TestRestore_TombstoneRemoveMissingFile(t *testing.T) {
	// tombstone на файл, которого нет в dest: Remove падает — Warn, не фатально
	h := newHarness(t)
	h.addSession(t, 1, []domain.FileMeta{
		{Path: "/etc/gone", State: domain.StateDeleted},
		meta("/etc/keep"),
	})
	if _, err := h.uc.Full(context.Background()); err != nil {
		t.Fatalf("Full: %v", err)
	}
}

// failCat — каталог с инъекцией сбоев.
type failCat struct {
	testutil.MemCatalog
	listSessions error
	copies       error
}

func (c *failCat) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	if c.listSessions != nil {
		return nil, c.listSessions
	}
	return c.MemCatalog.ListSessions(ctx, tapeUUID)
}

func (c *failCat) GetAllFileCopies(ctx context.Context, path string) ([]port.FileCopy, error) {
	if c.copies != nil {
		return nil, c.copies
	}
	return c.MemCatalog.GetAllFileCopies(ctx, path)
}
