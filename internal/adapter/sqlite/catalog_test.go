package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// newCatalog открывает каталог в памяти (через port.Catalog — тесты
// проверяют контракт порта) и регистрирует одну кассету.
func newCatalog(t *testing.T) (port.Catalog, domain.Session) {
	t.Helper()
	c, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := c.RegisterTape(context.Background(), "tape-1", "media-001", 1700000000); err != nil {
		t.Fatalf("RegisterTape: %v", err)
	}
	return c, domain.Session{
		TapeUUID: "tape-1", Num: 1, Type: domain.SessionFull,
		Timestamp: 1700000100, JobRunID: "run-1",
	}
}

func mustSession(t *testing.T, c port.Catalog, sess domain.Session) int64 {
	t.Helper()
	id, err := c.CreateSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return id
}

func file(path, hash string, state domain.FileState) domain.FileMeta {
	return domain.FileMeta{Path: path, Size: 10, ModTime: 1700000050_000000000, Hash: hash, State: state}
}

func TestNew_FileIsIdempotent(t *testing.T) {
	path := t.TempDir() + "/catalog.db"
	c1, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New #1: %v", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	c2, err := sqlite.New(path) // схема применяется повторно — без ошибки
	if err != nil {
		t.Fatalf("New #2: %v", err)
	}
	if err := c2.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}
}

func TestRegisterTape_Upsert(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := context.Background()

	rec, err := c.GetTapeByUUID(ctx, "tape-1")
	if err != nil {
		t.Fatalf("GetTapeByUUID: %v", err)
	}
	if rec.Name != "media-001" || rec.FormattedAt != 1700000000 {
		t.Errorf("record = %+v, want {media-001 1700000000}", rec)
	}

	if err := c.RegisterTape(ctx, "tape-1", "media-001-renamed", 1700009999); err != nil {
		t.Fatalf("re-RegisterTape: %v", err)
	}
	rec, err = c.GetTapeByUUID(ctx, "tape-1")
	if err != nil {
		t.Fatalf("GetTapeByUUID: %v", err)
	}
	if rec.Name != "media-001-renamed" || rec.FormattedAt != 1700009999 {
		t.Errorf("после upsert record = %+v, want обновлённые поля", rec)
	}
}

func TestGetTapeByUUID_NotFound(t *testing.T) {
	c, _ := newCatalog(t)
	_, err := c.GetTapeByUUID(context.Background(), "нет-такой")
	var nfe *domain.TapeNotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v, want *domain.TapeNotFoundError", err)
	}
	if nfe.UUID != "нет-такой" {
		t.Errorf("UUID = %q, want %q", nfe.UUID, "нет-такой")
	}
}

func TestCreateSession_HappyPath(t *testing.T) {
	c, sess := newCatalog(t)
	id, err := c.CreateSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id <= 0 {
		t.Errorf("id = %d, want > 0", id)
	}
}

func TestCreateSession_ForeignKeyWithoutTape(t *testing.T) {
	c, sess := newCatalog(t)
	sess.TapeUUID = "неизвестная-кассета"
	_, err := c.CreateSession(context.Background(), sess)
	if err == nil {
		t.Fatal("err = nil, want FOREIGN KEY violation (PRAGMA foreign_keys включён)")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("err = %v, want упоминание FOREIGN KEY", err)
	}
}

func TestCreateSession_DuplicateNumberRejected(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	if _, err := c.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession #1: %v", err)
	}
	sess.JobRunID = "run-2"
	if _, err := c.CreateSession(ctx, sess); err == nil {
		t.Fatal("дубликат (tape_uuid, session_num) принят, want UNIQUE violation")
	}
}

func TestCreateSession_BadTypeRejected(t *testing.T) {
	c, sess := newCatalog(t)
	sess.Type = "DIFF"
	if _, err := c.CreateSession(context.Background(), sess); err == nil {
		t.Fatal("недопустимый type принят, want CHECK violation")
	}
}

func TestLastSessionNum(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()

	got, err := c.LastSessionNum(ctx, "tape-1")
	if err != nil {
		t.Fatalf("LastSessionNum пустой кассеты: %v", err)
	}
	if got != 0 {
		t.Errorf("пустая кассета: num = %d, want 0", got)
	}

	for i := int32(1); i <= 3; i++ {
		s := sess
		s.Num = i
		s.JobRunID = "run"
		if _, err := c.CreateSession(ctx, s); err != nil {
			t.Fatalf("CreateSession #%d: %v", i, err)
		}
	}
	got, err = c.LastSessionNum(ctx, "tape-1")
	if err != nil {
		t.Fatalf("LastSessionNum: %v", err)
	}
	if got != 3 {
		t.Errorf("num = %d, want 3", got)
	}
}

func TestSaveFiles_Roundtrip(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	id := mustSession(t, c, sess)

	want := []domain.FileMeta{
		{Path: "/dir", IsDir: true, State: domain.StateAdded},
		file("/dir/a.txt", "aa", domain.StateAdded),
		file("/dir/b.txt", "bb", domain.StateModified),
		{Path: "/dir/gone.txt", State: domain.StateDeleted},
	}
	if err := c.SaveFiles(ctx, id, want); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	got, err := c.GetFilesBySession(ctx, id)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("файл[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSaveFiles_EmptyListIsNoop(t *testing.T) {
	c, sess := newCatalog(t)
	id := mustSession(t, c, sess)
	if err := c.SaveFiles(context.Background(), id, nil); err != nil {
		t.Fatalf("SaveFiles(nil): %v", err)
	}
	files, err := c.GetFilesBySession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("файлов = %d, want 0", len(files))
	}
}

func TestSaveFiles_RollbackOnError(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	id := mustSession(t, c, sess)

	ctxCanceled, cancel := context.WithCancel(ctx)
	cancel()
	batch := []domain.FileMeta{
		file("/ok.txt", "aa", domain.StateAdded),
		file("/bad.txt", "bb", domain.StateAdded),
	}
	if err := c.SaveFiles(ctxCanceled, id, batch); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	files, err := c.GetFilesBySession(ctx, id)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("после отката файлов = %d, want 0", len(files))
	}
}

func TestSaveFiles_BadStateRejected(t *testing.T) {
	c, sess := newCatalog(t)
	id := mustSession(t, c, sess)
	bad := file("/x", "aa", "X")
	if err := c.SaveFiles(context.Background(), id, []domain.FileMeta{bad}); err == nil {
		t.Fatal("недопустимый state принят, want CHECK violation")
	}
}

func TestGetLatestFileStates(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()

	old := sess
	old.Num, old.Timestamp, old.JobRunID = 1, 1000, "run-1"
	oldID := mustSession(t, c, old)
	if err := c.SaveFiles(ctx, oldID, []domain.FileMeta{
		file("/same.txt", "старый-хеш", domain.StateAdded),
		file("/only-old.txt", "хеш", domain.StateAdded),
	}); err != nil {
		t.Fatalf("SaveFiles old: %v", err)
	}

	recent := sess
	recent.Num, recent.Timestamp, recent.JobRunID = 2, 2000, "run-2"
	recentID := mustSession(t, c, recent)
	if err := c.SaveFiles(ctx, recentID, []domain.FileMeta{
		file("/same.txt", "новый-хеш", domain.StateModified),
		file("/new.txt", "хеш2", domain.StateAdded),
	}); err != nil {
		t.Fatalf("SaveFiles recent: %v", err)
	}

	states, err := c.GetLatestFileStates(ctx, []string{"/same.txt", "/only-old.txt", "/new.txt", "/нет-файла"})
	if err != nil {
		t.Fatalf("GetLatestFileStates: %v", err)
	}
	if got := states["/same.txt"].Hash; got != "новый-хеш" {
		t.Errorf("same.txt hash = %q, want позднейшая сессия", got)
	}
	if got := states["/same.txt"].State; got != domain.StateModified {
		t.Errorf("same.txt state = %q, want %q", got, domain.StateModified)
	}
	if got := states["/only-old.txt"].Hash; got != "хеш" {
		t.Errorf("only-old.txt hash = %q, want старая сессия", got)
	}
	if got := states["/new.txt"].Size; got != 10 {
		t.Errorf("new.txt size = %d, want 10", got)
	}
	if _, ok := states["/нет-файла"]; ok {
		t.Error("нет-файла попал в карту, want отсутствовать")
	}
}

func TestGetLatestFileStates_EmptyInput(t *testing.T) {
	c, _ := newCatalog(t)
	states, err := c.GetLatestFileStates(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetLatestFileStates(nil): %v", err)
	}
	if len(states) != 0 {
		t.Errorf("len = %d, want 0", len(states))
	}
}

func TestGetLatestFileStates_MoreThanOneBatch(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	id := mustSession(t, c, sess)

	const n = 750 // больше batchSize=500 внутри адаптера
	batch := make([]domain.FileMeta, 0, n)
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := "/f/" + strings.Repeat("x", 4) + strings.Repeat(string(rune('a'+i%26)), 3) + itoa(i)
		batch = append(batch, file(p, "hash", domain.StateAdded))
		paths = append(paths, p)
	}
	if err := c.SaveFiles(ctx, id, batch); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	states, err := c.GetLatestFileStates(ctx, paths)
	if err != nil {
		t.Fatalf("GetLatestFileStates: %v", err)
	}
	if len(states) != n {
		t.Errorf("len = %d, want %d", len(states), n)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func TestListTapes(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := context.Background()
	if err := c.RegisterTape(ctx, "tape-2", "aaa-000", 1); err != nil {
		t.Fatal(err)
	}
	tapes, err := c.ListTapes(ctx)
	if err != nil {
		t.Fatalf("ListTapes: %v", err)
	}
	if len(tapes) != 2 {
		t.Fatalf("len = %d, want 2", len(tapes))
	}
	if tapes[0].Name != "aaa-000" || tapes[1].Name != "media-001" {
		t.Errorf("порядок = [%s, %s], want по имени", tapes[0].Name, tapes[1].Name)
	}
}

func TestListSessions_FilterAndOrder(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	if err := c.RegisterTape(ctx, "tape-2", "media-002", 1); err != nil {
		t.Fatal(err)
	}
	for i := int32(1); i <= 2; i++ {
		s := sess
		s.Num = i
		s.JobRunID = "run-a"
		if _, err := c.CreateSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	other := sess
	other.TapeUUID, other.Num, other.JobRunID = "tape-2", 1, "run-b"
	if _, err := c.CreateSession(ctx, other); err != nil {
		t.Fatal(err)
	}

	got, err := c.ListSessions(ctx, "tape-1")
	if err != nil {
		t.Fatalf("ListSessions(tape-1): %v", err)
	}
	if len(got) != 2 || got[0].Num != 1 || got[1].Num != 2 {
		t.Fatalf("ListSessions(tape-1) = %+v, want 2 сессии по возрастанию номера", got)
	}

	all, err := c.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("ListSessions(all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListSessions(all) len = %d, want 3", len(all))
	}
	if all[0].TapeUUID != "tape-1" || all[2].TapeUUID != "tape-2" {
		t.Errorf("ListSessions(all) порядок: %+v", all)
	}
}

func TestGetFilesBySession_NotFound(t *testing.T) {
	c, _ := newCatalog(t)
	_, err := c.GetFilesBySession(context.Background(), 999)
	var nfe *domain.SessionNotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v, want *domain.SessionNotFoundError", err)
	}
}

func TestGetAllFileCopies_NewestFirst(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()

	for i, ts := range []int64{1000, 3000, 2000} {
		s := sess
		s.Num = int32(i + 1)
		s.Timestamp = ts
		s.JobRunID = "run"
		id := mustSession(t, c, s)
		state := domain.StateAdded
		if i > 0 {
			state = domain.StateModified
		}
		if err := c.SaveFiles(ctx, id, []domain.FileMeta{file("/movie.mkv", "хеш", state)}); err != nil {
			t.Fatal(err)
		}
	}

	copies, err := c.GetAllFileCopies(ctx, "/movie.mkv")
	if err != nil {
		t.Fatalf("GetAllFileCopies: %v", err)
	}
	if len(copies) != 3 {
		t.Fatalf("len = %d, want 3", len(copies))
	}
	wantTS := []int64{3000, 2000, 1000}
	for i, want := range wantTS {
		if copies[i].Timestamp != want {
			t.Errorf("copies[%d].Timestamp = %d, want %d (убывание)", i, copies[i].Timestamp, want)
		}
	}
	if copies[0].Meta.State != domain.StateModified || copies[0].TapeUUID != "tape-1" {
		t.Errorf("copies[0] = %+v, want данные сессии", copies[0])
	}
}

func TestSearchFiles(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	id := mustSession(t, c, sess)
	if err := c.SaveFiles(ctx, id, []domain.FileMeta{
		file("/tank/media/movie.mkv", "h1", domain.StateAdded),
		file("/tank/docs/report.pdf", "h2", domain.StateAdded),
	}); err != nil {
		t.Fatal(err)
	}

	copies, err := c.SearchFiles(ctx, "media")
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if len(copies) != 1 || copies[0].Meta.Path != "/tank/media/movie.mkv" {
		t.Fatalf("SearchFiles(media) = %+v, want один movie.mkv", copies)
	}
	if copies[0].SessionID != id {
		t.Errorf("SessionID = %d, want %d", copies[0].SessionID, id)
	}
}

func TestSearchFiles_EscapesLikeWildcards(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	id := mustSession(t, c, sess)
	if err := c.SaveFiles(ctx, id, []domain.FileMeta{
		file("/tank/media/movie.mkv", "h1", domain.StateAdded),
		file("/tank/docs/report.pdf", "h2", domain.StateAdded),
	}); err != nil {
		t.Fatal(err)
	}

	// Шаблон "%" без экранирования матчил бы оба файла; с экранированием —
	// ни одного (буквального процента в путях нет).
	copies, err := c.SearchFiles(ctx, "%")
	if err != nil {
		t.Fatalf("SearchFiles(%%): %v", err)
	}
	if len(copies) != 0 {
		t.Errorf("SearchFiles(%%) нашёл %d, want 0 (процент экранирован)", len(copies))
	}

	copies, err = c.SearchFiles(ctx, ".m")
	if err != nil {
		t.Fatalf("SearchFiles(.m): %v", err)
	}
	if len(copies) != 1 || copies[0].Meta.Path != "/tank/media/movie.mkv" {
		t.Errorf("SearchFiles(.m) = %+v, want movie.mkv", copies)
	}
}

func TestDeleteSession_CascadesFiles(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	id := mustSession(t, c, sess)
	if err := c.SaveFiles(ctx, id, []domain.FileMeta{file("/a.txt", "h", domain.StateAdded)}); err != nil {
		t.Fatal(err)
	}

	if err := c.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := c.GetFilesBySession(ctx, id); !errors.As(err, new(*domain.SessionNotFoundError)) {
		t.Errorf("после удаления GetFilesBySession = %v, want SessionNotFoundError", err)
	}
	// Каскад: файлы сессии удалены из таблицы files.
	if _, err := c.GetAllFileCopies(ctx, "/a.txt"); err != nil {
		t.Fatalf("GetAllFileCopies: %v", err)
	}
	states, err := c.GetLatestFileStates(ctx, []string{"/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Errorf("после каскада файлов = %d, want 0", len(states))
	}
}

func TestDeleteSession_NotFound(t *testing.T) {
	c, _ := newCatalog(t)
	err := c.DeleteSession(context.Background(), 42)
	if !errors.As(err, new(*domain.SessionNotFoundError)) {
		t.Fatalf("err = %v, want *domain.SessionNotFoundError", err)
	}
}

func TestPruneSessions(t *testing.T) {
	c, sess := newCatalog(t)
	ctx := context.Background()
	old := sess
	old.Num, old.Timestamp, old.JobRunID = 1, 1000, "run-1"
	mustSession(t, c, old)
	fresh := sess
	fresh.Num, fresh.Timestamp, fresh.JobRunID = 2, 9999999999, "run-2"
	freshID := mustSession(t, c, fresh)

	n, err := c.PruneSessions(ctx, 5000)
	if err != nil {
		t.Fatalf("PruneSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("удалено = %d, want 1", n)
	}
	remaining, err := c.ListSessions(ctx, "tape-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != freshID {
		t.Errorf("осталось = %+v, want только свежая сессия %d", remaining, freshID)
	}
	if n, err = c.PruneSessions(ctx, 0); err != nil || n != 0 {
		t.Errorf("PruneSessions(0) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestRegisterTape_CanceledContext(t *testing.T) {
	c, _ := newCatalog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.RegisterTape(ctx, "u", "n", 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
