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

package backup_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
)

var fixedTime = time.Unix(1700000000, 0).UTC()

// fakeConfig — ConfigSource с одним заданием.
type fakeConfig struct {
	jobs        []domain.Job
	err         error
	capacity    int64
	minTail     int64
	capacityErr error
	minTailErr  error
}

func (c *fakeConfig) Jobs() ([]domain.Job, error) { return c.jobs, c.err }
func (c *fakeConfig) Device() string              { return "/dev/nst0" }
func (c *fakeConfig) DB() string                  { return "db" }
func (c *fakeConfig) Log() string                 { return "log" }
func (c *fakeConfig) Server() string              { return "srv" }
func (c *fakeConfig) LogLevel() string            { return "info" }
func (c *fakeConfig) Capacity() (int64, error)    { return c.capacity, c.capacityErr }
func (c *fakeConfig) MinTail() (int64, error)     { return c.minTail, c.minTailErr }

func jobsAppend() []domain.Job {
	return []domain.Job{{Name: "daily", Mode: domain.ModeAppend, Paths: []string{"/etc"}}}
}

// recTape — FakeTape, запоминающий аргументы ForwardFilemarks и умеющий
// ронять k-й WriteEOF (failEOFOn > 0). ops — журнал операций ленты
// (rewind/fsf:N/weof) для сверки последовательности отката.
type recTape struct {
	*testutil.FakeTape
	fsf       []int
	failEOFOn int
	eofErr    error
	eofCalls  int
	ops       []string
}

func (t *recTape) ForwardFilemarks(ctx context.Context, n int) error {
	t.fsf = append(t.fsf, n)
	t.ops = append(t.ops, "fsf:"+fmt.Sprintf("%d", n))
	return t.FakeTape.ForwardFilemarks(ctx, n)
}

func (t *recTape) WriteEOF(ctx context.Context) error {
	t.eofCalls++
	t.ops = append(t.ops, "weof")
	if t.failEOFOn > 0 && t.eofCalls == t.failEOFOn {
		return t.eofErr
	}
	return t.FakeTape.WriteEOF(ctx)
}

func (t *recTape) Rewind(ctx context.Context) error {
	t.ops = append(t.ops, "rewind")
	return t.FakeTape.Rewind(ctx)
}

// harness — собранное окружение одного запуска.
type harness struct {
	fakeTape    *testutil.FakeTape
	rec         *recTape
	tapeOver    port.Tape // подмена ленты для сценариев сбоев
	codec       *testutil.FakeCodec
	cat         *testutil.MemCatalog
	overrideCat port.Catalog
	cfg         *fakeConfig // nil — обычный fakeConfig с jobsAppend
	rand        port.Rand
	fs          *testutil.MapFS
	uc          *backup.UseCase
	prog        *progRecorder
}

func (h *harness) tape() port.Tape {
	switch {
	case h.tapeOver != nil:
		return h.tapeOver
	case h.rec != nil:
		return h.rec
	default:
		return h.fakeTape
	}
}

// swapTape подменяет ленту, сохранив содержимое (включая ярлык).
func (h *harness) swapTape(wrap func(inner *testutil.FakeTape) port.Tape) {
	h.tapeOver = wrap(h.fakeTape)
}

// overrideTape устанавливает произвольную ленту (без сохранения).
func (h *harness) overrideTape(t port.Tape) {
	h.tapeOver = t
}

// rebuild пересобирает use case из текущих компонентов harness'а.
func (h *harness) rebuild() {
	h.uc = h.build(h.tape())
}

func (h *harness) build(t port.Tape) *backup.UseCase {
	cat := port.Catalog(h.cat)
	if h.overrideCat != nil {
		cat = h.overrideCat
	}
	rnd := h.rand
	if rnd == nil {
		rnd = testutil.FixedRand("run-uuid")
	}
	cfg := h.cfg
	if cfg == nil {
		cfg = &fakeConfig{jobs: jobsAppend()}
	}
	return backup.New(cfg, t, h.codec, cat, h.fs,
		testutil.HashFunc(func(r io.Reader) (string, error) { return "h", nil }),
		rnd, testutil.FixedClock(fixedTime), h.prog, testutil.NoopLogger())
}

// progRecorder собирает события прогресса.
type progRecorder struct {
	updates []port.ProgressUpdate
	done    int
	fails   int
}

func (p *progRecorder) Update(u port.ProgressUpdate) { p.updates = append(p.updates, u) }
func (p *progRecorder) Done()                        { p.done++ }
func (p *progRecorder) Fail(error)                   { p.fails++ }

// labelTape пишет на ленту ярлык + двойной EOF.
func labelTape(t *testing.T, tape *testutil.FakeTape, codec *testutil.FakeCodec) domain.TapeLabel {
	t.Helper()
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
	return label
}

func newHarness(t *testing.T, files map[string]string) *harness {
	t.Helper()
	fake := testutil.NewFakeTape()
	codec := &testutil.FakeCodec{}
	cat := testutil.NewMemCatalog()
	label := labelTape(t, fake, codec)
	if err := cat.RegisterTape(context.Background(), label.UUID, label.Name, 0); err != nil {
		t.Fatalf("RegisterTape: %v", err)
	}
	h := &harness{
		fakeTape: fake,
		rec:      &recTape{FakeTape: fake},
		codec:    codec,
		cat:      cat,
		fs:       testutil.NewMapFS(files),
		prog:     &progRecorder{},
	}
	h.uc = h.build(h.rec)
	return h
}

func TestBackup_FirstSessionIsFull(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if res.Session.Num != 1 || res.Session.Type != domain.SessionFull {
		t.Fatalf("сессия: %+v; want Num=1 FULL", res.Session)
	}
	if res.Session.TapeUUID != "tape-uuid" || res.Session.JobRunID != "run-uuid" {
		t.Errorf("сессия: %+v", res.Session)
	}
	if res.Session.Timestamp != fixedTime.Unix() {
		t.Errorf("Timestamp = %d; want %d", res.Session.Timestamp, fixedTime.Unix())
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 1 || sessions[0].ID != res.Session.ID {
		t.Fatalf("ListSessions: %+v", sessions)
	}
	files, err := h.cat.GetFilesBySession(context.Background(), res.Session.ID)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	if len(files) != 2 { // /etc + /etc/hosts
		t.Fatalf("файлов в каталоге %d; want 2: %+v", len(files), files)
	}
	if len(h.codec.WroteHeaders) != 1 {
		t.Fatalf("WriteSession вызовов %d; want 1", len(h.codec.WroteHeaders))
	}
	hdr := h.codec.WroteHeaders[0]
	if hdr.SessionNum != 1 || hdr.Type != domain.SessionFull || hdr.JobName != "daily" || hdr.JobRunID != "run-uuid" {
		t.Errorf("заголовок: %+v", hdr)
	}
	// позиционирование MTFSF(1); FakeCodec не пишет блоки — только EOD-пара
	if len(h.rec.fsf) != 1 || h.rec.fsf[0] != 1 {
		t.Errorf("ForwardFilemarks args = %v; want [1]", h.rec.fsf)
	}
	if got := h.fakeTape.MarkCount(); got != 3 {
		t.Errorf("MarkCount = %d; want 3", got)
	}
	if res.Stats.Added != 2 || res.Stats.Scanned != 2 || res.Stats.Bytes == 0 {
		t.Errorf("статистика: %+v", res.Stats)
	}
	if h.prog.done != 1 || h.prog.fails != 0 {
		t.Errorf("прогресс: done=%d fails=%d; want 1/0", h.prog.done, h.prog.fails)
	}
}

func TestBackup_IncrementAppendsSessionNum(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	// меняем файл: другой mtime → Modified
	h.fs.MapFS["etc/hosts"].ModTime = time.Unix(10, 0)
	h.codec.WroteHeaders = nil
	h.rec.fsf = nil

	res, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup#2: %v", err)
	}
	if res.Session.Num != 2 || res.Session.Type != domain.SessionInc {
		t.Fatalf("сессия: %+v; want Num=2 INC", res.Session)
	}
	if hdr := h.codec.WroteHeaders[0]; hdr.SessionNum != 2 || hdr.Type != domain.SessionInc {
		t.Errorf("заголовок: %+v", hdr)
	}
	if len(h.rec.fsf) != 1 || h.rec.fsf[0] != 3 {
		t.Errorf("ForwardFilemarks args = %v; want [3] (MTFSF(2K+1), K=1)", h.rec.fsf)
	}
	if res.Stats.Modified != 1 || res.Stats.Added != 0 || res.Stats.Scanned != 1 {
		t.Errorf("статистика: %+v; want Modified=1", res.Stats)
	}
	sessions, _ := h.cat.ListSessions(ctx, "tape-uuid")
	if len(sessions) != 2 {
		t.Fatalf("сессий в каталоге %d; want 2", len(sessions))
	}
}

func TestBackup_ForcedFullRestartsNumbering(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	h.fs.MapFS["etc/hosts"].ModTime = time.Unix(10, 0)

	res, err := h.uc.Backup(ctx, "daily", backup.Options{Full: true})
	if err != nil {
		t.Fatalf("Backup(Full): %v", err)
	}
	if res.Session.Num != 1 || res.Session.Type != domain.SessionFull {
		t.Fatalf("сессия: %+v; want Num=1 FULL", res.Session)
	}
	sessions, _ := h.cat.ListSessions(ctx, "tape-uuid")
	if len(sessions) != 1 || sessions[0].ID != res.Session.ID {
		t.Fatalf("старые сессии не удалены: %+v", sessions)
	}
}

func TestBackup_DryRun(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{DryRun: true})
	if err != nil {
		t.Fatalf("Backup(DryRun): %v", err)
	}
	if res.Session.ID != 0 {
		t.Errorf("DryRun вернул сессию: %+v", res.Session)
	}
	if res.Stats.Added == 0 {
		t.Errorf("статистика пуста: %+v", res.Stats)
	}
	if len(h.codec.WroteHeaders) != 0 {
		t.Errorf("DryRun писал на ленту: %d вызовов", len(h.codec.WroteHeaders))
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("DryRun создал сессии: %+v", sessions)
	}
	if h.prog.done != 1 {
		t.Errorf("done = %d; want 1", h.prog.done)
	}
}

func TestBackup_MirrorTombstoneStats(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/keep": "a", "etc/gone": "b"})
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	delete(h.fs.MapFS, "etc/gone")
	h.codec.WroteHeaders = nil
	h.rec.fsf = nil
	// пересобираем с mirror-заданием
	h.uc = backup.New(
		&fakeConfig{jobs: []domain.Job{{Name: "daily", Mode: domain.ModeMirror, Paths: []string{"/etc"}}}},
		h.tape(), h.codec, h.cat, h.fs,
		testutil.HashFunc(func(r io.Reader) (string, error) { return "h", nil }),
		testutil.FixedRand("run-2"), testutil.FixedClock(fixedTime),
		h.prog, testutil.NoopLogger())

	res, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup#2(mirror): %v", err)
	}
	if res.Stats.Deleted != 1 {
		t.Fatalf("Deleted = %d; want 1: %+v", res.Stats.Deleted, res.Stats)
	}
	sessFiles, err := h.cat.GetFilesBySession(ctx, res.Session.ID)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	var tomb int
	for _, fm := range sessFiles {
		if fm.IsDeleted() {
			tomb++
		}
	}
	if tomb != 1 {
		t.Errorf("tombstone'ов в каталоге %d; want 1", tomb)
	}
}

func TestBackup_JobNotFound(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.uc.Backup(context.Background(), "ghost", backup.Options{}); err == nil {
		t.Fatal("несуществующее задание должно давать ошибку")
	}
}

func TestBackup_BlankTape(t *testing.T) {
	h := newHarness(t, nil)
	h.overrideTape(testutil.NewFakeTape())
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if !errors.Is(err, &domain.BlankTapeError{}) {
		t.Fatalf("Backup: %v; want BlankTapeError", err)
	}
}

func TestBackup_TapeUnknownToCatalog(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	h.cat = testutil.NewMemCatalog()
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if !errors.Is(err, &domain.TapeNotFoundError{UUID: "tape-uuid"}) {
		t.Fatalf("Backup: %v; want TapeNotFoundError", err)
	}
}

func TestBackup_ForeignTape(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	foreign := testutil.NewFakeTape()
	if err := foreign.WriteBlock(context.Background(), []byte("not ours")); err != nil {
		t.Fatal(err)
	}
	h.overrideTape(foreign)
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if !errors.Is(err, &domain.ForeignFormatError{}) {
		t.Fatalf("Backup: %v; want ForeignFormatError", err)
	}
}

func TestBackup_WriteFailureCompensates(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	boom := errors.New("boom")
	h.codec.ErrWrite = boom
	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("Backup: %v; want boom", err)
	}
	if res.Session.ID != 0 {
		t.Errorf("сессия в результате ошибки: %+v", res.Session)
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("сессия не удалена из каталога: %+v", sessions)
	}
	if h.prog.fails != 1 || h.prog.done != 0 {
		t.Errorf("прогресс: fails=%d done=%d; want 1/0", h.prog.fails, h.prog.done)
	}
}

func TestBackup_TapeFullMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantFull bool
	}{
		{"typed TapeFullError", &domain.TapeFullError{Capacity: 100}, true},
		{"enosys string", errors.New("write block: no space left on device"), true},
		{"unrelated", errors.New("io error"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			h.codec.ErrWrite = tc.err
			_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
			var full *domain.TapeFullError
			if tc.wantFull && !errors.As(err, &full) {
				t.Fatalf("Backup: %v; want TapeFullError", err)
			}
			if !tc.wantFull && errors.As(err, &full) {
				t.Fatalf("Backup: %v; не должен быть TapeFullError", err)
			}
		})
	}
}

func TestBackup_EODPairFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		eofOn int
	}{
		{"first eod mark", 1},
		{"second eod mark", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			boom := errors.New("boom")
			// после ярлыка (2 EOF в labelTape, до создания recTape)
			// closeEOD даёт 1-й и 2-й WriteEOF на recTape
			h.rec.failEOFOn = tc.eofOn
			h.rec.eofErr = boom
			if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); !errors.Is(err, boom) {
				t.Fatalf("Backup: %v; want boom", err)
			}
			sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
			if len(sessions) != 0 {
				t.Errorf("сессия не откачена: %+v", sessions)
			}
		})
	}
}

// TestBackup_WriteFailureRestoresEOD — при сбое записи лента
// возвращается к старому EOD (сессия 1 плана spanning): после
// позиционирования записи — Rewind + MTFSF(2K+1) + пара WriteEOF,
// усекающая грязный хвост; повторный бекап на восстановленную ленту
// проходит.
func TestBackup_WriteFailureRestoresEOD(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	h.fs.MapFS["etc/hosts"].ModTime = time.Unix(10, 0)
	boom := errors.New("boom")
	h.codec.ErrWrite = boom
	h.rec.ops = nil

	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); !errors.Is(err, boom) {
		t.Fatalf("Backup#2: %v; want boom", err)
	}
	// readLabel: rewind; writeSession: rewind + MTFSF(2K+1)=3 (K=1);
	// восстановление: rewind, MTFSF(3) на старый EOD, пара WriteEOF.
	want := []string{"rewind", "rewind", "fsf:3", "rewind", "fsf:3", "weof", "weof"}
	if !slices.Equal(h.rec.ops, want) {
		t.Fatalf("операции ленты = %v; want %v", h.rec.ops, want)
	}
	// лента консистентна: K=1 → 2K+3 = 5 filemark'ов, грязного хвоста нет
	if got := h.fakeTape.MarkCount(); got != 5 {
		t.Errorf("MarkCount = %d; want 5", got)
	}
	sessions, _ := h.cat.ListSessions(ctx, "tape-uuid")
	if len(sessions) != 1 {
		t.Errorf("сессий в каталоге %d; want 1", len(sessions))
	}

	// дозапись на восстановленную ленту проходит
	h.codec.ErrWrite = nil
	res, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup#3 на восстановленной ленте: %v", err)
	}
	if res.Session.Num != 2 || res.Session.Type != domain.SessionInc {
		t.Errorf("сессия #3: %+v; want Num=2 INC", res.Session)
	}
}

// TestBackup_RestoreEODFailuresJoined — ошибки восстановления ленты
// не глотаются: присоединяются к исходной ошибке записи, ошибки после
// перемотки несут рекомендацию оператору.
func TestBackup_RestoreEODFailuresJoined(t *testing.T) {
	boom, boom2 := errors.New("boom"), errors.New("boom2")
	cases := []struct {
		name       string
		prep       func(t *testing.T, h *harness)
		wantAdvice bool
	}{
		{
			name: "rewind fails",
			prep: func(t *testing.T, h *harness) {
				h.codec.ErrWrite = boom
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, rewindFail: 3, rewindErr: boom2}
				})
			},
		},
		{
			name: "positioning fails",
			prep: func(t *testing.T, h *harness) {
				h.codec.ErrWrite = boom
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, fsfFail: 2, fsfErr: boom2}
				})
			},
			wantAdvice: true,
		},
		{
			name: "eod mark fails",
			prep: func(t *testing.T, h *harness) {
				h.codec.ErrWrite = boom
				h.rec.failEOFOn = 1 // первый WriteEOF восстановления
				h.rec.eofErr = boom2
			},
			wantAdvice: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			tc.prep(t, h)
			h.rebuild()
			_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
			if !errors.Is(err, boom) || !errors.Is(err, boom2) {
				t.Fatalf("Backup: %v; want joined boom+boom2", err)
			}
			if tc.wantAdvice && !strings.Contains(err.Error(), "tape readtest") {
				t.Fatalf("Backup: ошибка %v без рекомендации readtest", err)
			}
			sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
			if len(sessions) != 0 {
				t.Errorf("сессия не откачена из каталога: %+v", sessions)
			}
		})
	}
}

func TestBackup_SaveFilesFailure(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	boom := errors.New("boom")
	h.overrideCat = &failCatalogBy{MemCatalog: h.cat, saveFiles: boom}
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("Backup: %v; want boom", err)
	}
}

func TestBackup_ScanFailure(t *testing.T) {
	h := newHarness(t, nil) // корень /etc отсутствует
	if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err == nil {
		t.Fatal("scan по отсутствующему корню должен падать")
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("сессия создана несмотря на ошибку скана: %+v", sessions)
	}
}

func TestBackup_CanceledContext(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backup: %v; want context.Canceled", err)
	}
}

func TestBackup_ConfigError(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	boom := errors.New("boom")
	h.uc = backup.New(&fakeConfig{err: boom}, h.rec, h.codec, h.cat, h.fs,
		nil, testutil.FixedRand("u"), testutil.FixedClock(fixedTime), nil, testutil.NoopLogger())
	if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); !errors.Is(err, boom) {
		t.Fatalf("Backup: %v; want boom", err)
	}
}

// failCatalogBy — MemCatalog, роняющий выбранные методы.
type failCatalogBy struct {
	*testutil.MemCatalog
	listSessions  error
	getFiles      error
	getFilesFor   int64 // с getFiles: падает только сессия с этим PK
	deleteSession error
	createSession error
	saveFiles     error
}

func (c *failCatalogBy) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	if c.listSessions != nil {
		return nil, c.listSessions
	}
	return c.MemCatalog.ListSessions(ctx, tapeUUID)
}

func (c *failCatalogBy) GetFilesBySession(ctx context.Context, sessionID int64) ([]domain.FileMeta, error) {
	if c.getFiles != nil && (c.getFilesFor == 0 || c.getFilesFor == sessionID) {
		return nil, c.getFiles
	}
	return c.MemCatalog.GetFilesBySession(ctx, sessionID)
}

func (c *failCatalogBy) DeleteSession(ctx context.Context, sessionID int64) error {
	if c.deleteSession != nil {
		return c.deleteSession
	}
	return c.MemCatalog.DeleteSession(ctx, sessionID)
}

func (c *failCatalogBy) CreateSession(ctx context.Context, sess domain.Session) (int64, error) {
	if c.createSession != nil {
		return 0, c.createSession
	}
	return c.MemCatalog.CreateSession(ctx, sess)
}

func (c *failCatalogBy) SaveFiles(ctx context.Context, sessionID int64, files []domain.FileMeta) error {
	if c.saveFiles != nil {
		return c.saveFiles
	}
	return c.MemCatalog.SaveFiles(ctx, sessionID, files)
}

// failTapeBy — FakeTape, роняющий выбранные операции. fsfErr без
// fsfFail роняет каждый ForwardFilemarks, с fsfFail — только k-й
// (аналогично rewindFail).
type failTapeBy struct {
	*testutil.FakeTape
	readErr    error
	rewindErr  error
	fsfErr     error
	rewindCnt  int
	rewindFail int
	fsfCnt     int
	fsfFail    int
}

func (t *failTapeBy) ReadBlock(ctx context.Context) ([]byte, error) {
	if t.readErr != nil {
		return nil, t.readErr
	}
	return t.FakeTape.ReadBlock(ctx)
}

func (t *failTapeBy) Rewind(ctx context.Context) error {
	t.rewindCnt++
	if t.rewindErr != nil && (t.rewindFail == 0 || t.rewindFail == t.rewindCnt) {
		return t.rewindErr
	}
	return t.FakeTape.Rewind(ctx)
}

func (t *failTapeBy) ForwardFilemarks(ctx context.Context, n int) error {
	t.fsfCnt++
	if t.fsfErr != nil && (t.fsfFail == 0 || t.fsfFail == t.fsfCnt) {
		return t.fsfErr
	}
	return t.FakeTape.ForwardFilemarks(ctx, n)
}

func TestBackup_ErrorPaths(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		full bool
		prep func(t *testing.T, h *harness)
	}{
		{
			name: "read block error",
			prep: func(t *testing.T, h *harness) {
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, readErr: boom}
				})
			},
		},
		{
			name: "uuid error",
			prep: func(t *testing.T, h *harness) {
				h.rand = testutil.FailingRand(boom)
			},
		},
		{
			name: "list sessions error",
			prep: func(t *testing.T, h *harness) {
				h.overrideCat = &failCatalogBy{MemCatalog: h.cat, listSessions: boom}
			},
		},
		{
			name: "snapshot read error",
			prep: func(t *testing.T, h *harness) {
				if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err != nil {
					t.Fatalf("Backup#1: %v", err)
				}
				h.overrideCat = &failCatalogBy{MemCatalog: h.cat, getFiles: boom}
			},
		},
		{
			name: "create session error",
			prep: func(t *testing.T, h *harness) {
				h.overrideCat = &failCatalogBy{MemCatalog: h.cat, createSession: boom}
			},
		},
		{
			name: "rewind before write error",
			prep: func(t *testing.T, h *harness) {
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, rewindFail: 2, rewindErr: boom}
				})
			},
		},
		{
			name: "positioning error",
			prep: func(t *testing.T, h *harness) {
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, fsfErr: boom}
				})
			},
		},
		{
			name: "compensation delete error",
			prep: func(t *testing.T, h *harness) {
				h.codec.ErrWrite = boom
				h.overrideCat = &failCatalogBy{MemCatalog: h.cat, deleteSession: errors.New("rollback")}
			},
		},
		{
			name: "drop sessions error",
			full: true,
			prep: func(t *testing.T, h *harness) {
				if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err != nil {
					t.Fatalf("Backup#1: %v", err)
				}
				h.overrideCat = &failCatalogBy{MemCatalog: h.cat, deleteSession: boom}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			tc.prep(t, h)
			h.rebuild()
			if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{Full: tc.full}); err == nil {
				t.Fatal("ожидалась ошибка")
			}
		})
	}
}

// spanFiles — три файла по 60/50/60 байт: при бюджете 100 режутся
// на 3 части.
func spanFiles() map[string]string {
	return map[string]string{
		"etc/a": strings.Repeat("a", 60),
		"etc/b": strings.Repeat("b", 50),
		"etc/c": strings.Repeat("c", 60),
	}
}

// TestBackup_PlannerDryRun — планировщик в DryRun: части посчитаны,
// бюджет полной ёмкости (первая сессия), лента и каталог не тронуты.
func TestBackup_PlannerDryRun(t *testing.T) {
	h := newHarness(t, spanFiles())
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 100}
	h.rebuild()
	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{DryRun: true})
	if err != nil {
		t.Fatalf("Backup(DryRun): %v", err)
	}
	if res.PlannedParts != 3 {
		t.Errorf("PlannedParts = %d; want 3 (60/50/60 при бюджете 100)", res.PlannedParts)
	}
	if res.PlannedByBudget {
		t.Error("PlannedByBudget = true; want false (первая сессия — бюджет capacity)")
	}
	if len(h.codec.WroteHeaders) != 0 || h.fakeTape.MarkCount() != 2 {
		t.Errorf("DryRun тронул ленту: записей %d, меток %d", len(h.codec.WroteHeaders), h.fakeTape.MarkCount())
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("DryRun создал сессии: %+v", sessions)
	}
}

// TestBackup_FileTooLargeBeforeWrite — файл больше кассеты ловится
// планировщиком до записи: лента и каталог не тронуты.
func TestBackup_FileTooLargeBeforeWrite(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": strings.Repeat("x", 150)})
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 100}
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	var tooBig *domain.FileTooLargeError
	if !errors.As(err, &tooBig) {
		t.Fatalf("Backup: %v; want FileTooLargeError", err)
	}
	if tooBig.Path != "/etc/hosts" || tooBig.Size != 150 || tooBig.Capacity != 100 {
		t.Errorf("FileTooLargeError = %+v; want /etc/hosts 150/100", tooBig)
	}
	if len(h.codec.WroteHeaders) != 0 || h.fakeTape.MarkCount() != 2 {
		t.Errorf("лента тронута: записей %d, меток %d; want 0/2 (только ярлык)",
			len(h.codec.WroteHeaders), h.fakeTape.MarkCount())
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("сессия создана перед записью: %+v", sessions)
	}
	if h.prog.fails != 0 || h.prog.done != 0 {
		t.Errorf("прогресс: fails=%d done=%d; want 0/0 (сбой до записи)", h.prog.fails, h.prog.done)
	}
}

// TestBackup_MultiPartContinuesSingleTape — части >1 без spanning:
// поведение не меняется, сессия пишется целиком одной кассетой.
func TestBackup_MultiPartContinuesSingleTape(t *testing.T) {
	h := newHarness(t, spanFiles())
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 100}
	h.rebuild()
	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if res.PlannedParts != 3 {
		t.Errorf("PlannedParts = %d; want 3", res.PlannedParts)
	}
	if res.Session.Num != 1 {
		t.Errorf("сессия: %+v; want Num=1", res.Session)
	}
	files, err := h.cat.GetFilesBySession(context.Background(), res.Session.ID)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	if len(files) != 4 { // /etc + три файла
		t.Errorf("файлов в каталоге %d; want 4", len(files))
	}
}

// TestBackup_AppendBudgetFromRemainder — бюджет дозаписи: capacity
// минус Σ байт прошлых сессий (tombstone'ы не считаются).
func TestBackup_AppendBudgetFromRemainder(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": strings.Repeat("x", 100)})
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 1000, minTail: 10}
	h.rebuild()
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	// новый файл 200 байт: остаток 1000−100=900 вмещает его целиком
	h.fs.MapFS["etc/new"] = &fstest.MapFile{Data: []byte(strings.Repeat("n", 200)), Mode: 0o644}
	res, err := h.uc.Backup(ctx, "daily", backup.Options{DryRun: true})
	if err != nil {
		t.Fatalf("Backup#2(DryRun): %v", err)
	}
	if !res.PlannedByBudget {
		t.Error("PlannedByBudget = false; want true (дозапись в остаток)")
	}
	if res.PlannedParts != 1 {
		t.Errorf("PlannedParts = %d; want 1", res.PlannedParts)
	}
}

// TestBackup_AppendMinTailForcesNewTape — остаток меньше min_tail:
// бюджет 0, вся сессия на новую кассету (PlannedByBudget=false).
func TestBackup_AppendMinTailForcesNewTape(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": strings.Repeat("x", 100)})
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 1000, minTail: 950}
	h.rebuild()
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	h.fs.MapFS["etc/new"] = &fstest.MapFile{Data: []byte(strings.Repeat("n", 50)), Mode: 0o644}
	res, err := h.uc.Backup(ctx, "daily", backup.Options{DryRun: true})
	if err != nil {
		t.Fatalf("Backup#2(DryRun): %v", err)
	}
	if res.PlannedByBudget {
		t.Error("PlannedByBudget = true; want false (остаток 900 < min_tail 950)")
	}
	if res.PlannedParts != 1 {
		t.Errorf("PlannedParts = %d; want 1 (budget=0 — без деления)", res.PlannedParts)
	}
}

// TestBackup_AppendTapeOverCapacity — занято больше capacity:
// остаток зажимается нулём, без отрицательного бюджета.
func TestBackup_AppendTapeOverCapacity(t *testing.T) {
	h := newHarness(t, spanFiles()) // 170 байт при capacity 100
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 100}
	h.rebuild()
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	h.fs.MapFS["etc/hosts"] = &fstest.MapFile{Data: []byte("changed"), Mode: 0o644, ModTime: time.Unix(99, 0)}
	res, err := h.uc.Backup(ctx, "daily", backup.Options{DryRun: true})
	if err != nil {
		t.Fatalf("Backup#2(DryRun): %v", err)
	}
	if res.PlannedByBudget {
		t.Error("PlannedByBudget = true; want false (остаток зажат нулём)")
	}
	if res.PlannedParts != 1 {
		t.Errorf("PlannedParts = %d; want 1", res.PlannedParts)
	}
}

// TestBackup_PlannerConfigErrors — ошибки конфига spanning
// возвращаются до записи.
func TestBackup_PlannerConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  *fakeConfig
	}{
		{"capacity read fails", &fakeConfig{jobs: jobsAppend(), capacityErr: errors.New("boom")}},
		{"negative capacity", &fakeConfig{jobs: jobsAppend(), capacity: -1}},
		{"min_tail read fails", &fakeConfig{jobs: jobsAppend(), capacity: 1000, minTailErr: errors.New("boom")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			h.cfg = tc.cfg
			h.rebuild()
			if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err == nil {
				t.Fatal("ожидалась ошибка конфига spanning")
			}
			if len(h.codec.WroteHeaders) != 0 {
				t.Error("лента тронута несмотря на ошибку конфига")
			}
		})
	}
}

// TestBackup_PlannerCatalogError — сбой чтения файлов прошлой сессии
// для оценки остатка: ошибка до записи (не последняя сессия, чтобы
// пройти мимо lastSnapshot).
func TestBackup_PlannerCatalogError(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	ctx := context.Background()
	res1, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	h.fs.MapFS["etc/hosts"] = &fstest.MapFile{Data: []byte("ch"), Mode: 0o644, ModTime: time.Unix(10, 0)}
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#2: %v", err)
	}
	boom := errors.New("boom")
	h.overrideCat = &failCatalogBy{MemCatalog: h.cat, getFiles: boom, getFilesFor: res1.Session.ID}
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: 1000}
	h.rebuild()
	_, err = h.uc.Backup(ctx, "daily", backup.Options{DryRun: true})
	if !errors.Is(err, boom) {
		t.Fatalf("Backup#3: %v; want boom (ошибка оценки остатка)", err)
	}
	if !strings.Contains(err.Error(), "оценки остатка") {
		t.Errorf("ошибка %v не из usedBytes", err)
	}
}
