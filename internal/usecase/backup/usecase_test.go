package backup_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
)

var fixedTime = time.Unix(1700000000, 0).UTC()

// fakeConfig — ConfigSource с одним заданием.
type fakeConfig struct {
	jobs []domain.Job
	err  error
}

func (c *fakeConfig) Jobs() ([]domain.Job, error) { return c.jobs, c.err }
func (c *fakeConfig) Device() string              { return "/dev/nst0" }
func (c *fakeConfig) DB() string                  { return "db" }
func (c *fakeConfig) Log() string                 { return "log" }
func (c *fakeConfig) Server() string              { return "srv" }
func (c *fakeConfig) LogLevel() string            { return "info" }

func jobsAppend() []domain.Job {
	return []domain.Job{{Name: "daily", Mode: domain.ModeAppend, Paths: []string{"/etc"}}}
}

// recTape — FakeTape, запоминающий аргументы ForwardFilemarks и умеющий
// ронять k-й WriteEOF (failEOFOn > 0).
type recTape struct {
	*testutil.FakeTape
	fsf       []int
	failEOFOn int
	eofErr    error
	eofCalls  int
}

func (t *recTape) ForwardFilemarks(ctx context.Context, n int) error {
	t.fsf = append(t.fsf, n)
	return t.FakeTape.ForwardFilemarks(ctx, n)
}

func (t *recTape) WriteEOF(ctx context.Context) error {
	t.eofCalls++
	if t.failEOFOn > 0 && t.eofCalls == t.failEOFOn {
		return t.eofErr
	}
	return t.FakeTape.WriteEOF(ctx)
}

// harness — собранное окружение одного запуска.
type harness struct {
	fakeTape    *testutil.FakeTape
	rec         *recTape
	tapeOver    port.Tape // подмена ленты для сценариев сбоев
	codec       *testutil.FakeCodec
	cat         *testutil.MemCatalog
	overrideCat port.Catalog
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
	return backup.New(&fakeConfig{jobs: jobsAppend()}, t, h.codec, cat, h.fs,
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
	if c.getFiles != nil {
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

// failTapeBy — FakeTape, роняющий выбранные операции.
type failTapeBy struct {
	*testutil.FakeTape
	readErr    error
	rewindErr  error
	fsfErr     error
	rewindCnt  int
	rewindFail int
}

func (t *failTapeBy) ReadBlock(ctx context.Context) ([]byte, error) {
	if t.readErr != nil {
		return nil, t.readErr
	}
	return t.FakeTape.ReadBlock(ctx)
}

func (t *failTapeBy) Rewind(ctx context.Context) error {
	t.rewindCnt++
	if t.rewindErr != nil || t.rewindFail == t.rewindCnt {
		return t.rewindErr
	}
	return t.FakeTape.Rewind(ctx)
}

func (t *failTapeBy) ForwardFilemarks(ctx context.Context, n int) error {
	if t.fsfErr != nil {
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
