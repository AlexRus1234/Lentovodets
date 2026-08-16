// Тесты CLI: сценарии execute(args) -> (stdout, err) на фейках.

package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lentovodec/internal/adapter/tomlconfig"
	"lentovodec/internal/domain"
	"lentovodec/internal/iface/cli"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// fakeClient — заглушка ServerClient с готовыми данными.
type fakeClient struct {
	tapes    []port.TapeRecord
	sessions []domain.Session
	files    []domain.FileMeta
	copies   []port.FileCopy
	pruned   int64
}

func (c *fakeClient) TapeInfo(context.Context) (domain.TapeInfo, error) {
	return domain.TapeInfo{Label: domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: 2, Name: "T1", UUID: "u1",
		FormattedAt: "2026-01-01T00:00:00Z",
	}}, nil
}

func (c *fakeClient) Eject(context.Context) error { return nil }

func (c *fakeClient) ListTapes(context.Context) ([]port.TapeRecord, error) { return c.tapes, nil }

func (c *fakeClient) ListSessions(_ context.Context, tapeUUID string) ([]domain.Session, error) {
	if tapeUUID == "" {
		return c.sessions, nil
	}
	var out []domain.Session
	for _, s := range c.sessions {
		if s.TapeUUID == tapeUUID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *fakeClient) SessionFiles(context.Context, int64) ([]domain.FileMeta, error) {
	return c.files, nil
}

func (c *fakeClient) Search(context.Context, string) ([]port.FileCopy, error) {
	return c.copies, nil
}

func (c *fakeClient) DeleteSession(context.Context, int64) error { return nil }

func (c *fakeClient) Prune(context.Context, int64) (int64, error) { return c.pruned, nil }

// stubDaemon — Daemon, сразу завершающийся.
type stubDaemon struct{ bind string }

func (d stubDaemon) BindAddr() string          { return d.bind }
func (d stubDaemon) Run(context.Context) error { return nil }

// env — тестовое окружение одной команды.
type env struct {
	deps   cli.Deps
	cat    *testutil.MemCatalog
	codec  *testutil.FakeCodec
	cfgDir string
}

// newEnv создаёт окружение с конфигом toml во временном каталоге.
func newEnv(t *testing.T, toml string, client cli.ServerClient) *env {
	t.Helper()
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "lentovodec.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return envAt(t, cfgDir, client)
}

// envAt создаёт окружение на существующем каталоге конфига
// (свежий stdout/каталог/кодек — для пошаговых проверок).
func envAt(t *testing.T, cfgDir string, client cli.ServerClient) *env {
	t.Helper()
	cat := testutil.NewMemCatalog()
	codec := &testutil.FakeCodec{}
	deps := cli.Deps{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Stdin:   strings.NewReader(""),
		Version: "test",
		OpenConfig: func(p string, flags map[string]string) (cli.ConfigFile, error) {
			return tomlconfig.New(p, flags)
		},
		OpenTape:    func(string) (port.Tape, error) { return tapeFor(codec), nil },
		OpenCatalog: func(string) (port.Catalog, error) { return cat, nil },
		FS:          testutil.NewMapFS(nil),
		Codec:       codec,
		Hasher: testutil.HashFunc(func(io.Reader) (string, error) {
			return "0000000000000000", nil
		}),
		Rand:  testutil.FixedRand(tapeUUID),
		Clock: testutil.FixedClock(time.Unix(1700000000, 0)),
		DialServer: func(_, _, _ string, _ func() (string, error)) cli.ServerClient {
			return client
		},
		NewDaemon: func(cli.DaemonOpts) (cli.Daemon, error) {
			return stubDaemon{bind: "127.0.0.1:29201"}, nil
		},
		AskPassword: func() (string, error) { return "", nil },
	}
	return &env{deps: deps, cat: cat, codec: codec, cfgDir: cfgDir}
}

// tapeFor — фейк-лента с ярлыком кассеты (uuid FixedRand).
func tapeFor(codec *testutil.FakeCodec) *testutil.FakeTape {
	tp := testutil.NewFakeTape()
	block, err := codec.EncodeLabel(domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "T1", UUID: "11111111-1111-4111-8111-111111111111",
		FormattedAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	if err := tp.WriteBlock(ctx, block); err != nil {
		panic(err)
	}
	if err := tp.WriteEOF(ctx); err != nil {
		panic(err)
	}
	if err := tp.WriteEOF(ctx); err != nil {
		panic(err)
	}
	return tp
}

const tapeUUID = "11111111-1111-4111-8111-111111111111"

const jobTOML = `
[[jobs]]
name = "media"
description = "сериалы"
mode = "mirror"
paths = ["/data"]
exclude = ["*.tmp"]
`

// outOf выполняет команду окружения (с --config) и возвращает stdout.
func outOf(t *testing.T, e *env, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--config", filepath.Join(e.cfgDir, "lentovodec.toml")}, args...)
	err := cli.Execute(full, e.deps)
	return e.deps.Stdout.(*bytes.Buffer).String(), err
}

// registerTape регистрирует кассету ярлыка в каталоге.
func registerTape(t *testing.T, cat *testutil.MemCatalog) {
	t.Helper()
	if err := cat.RegisterTape(context.Background(), tapeUUID, "T1", 1); err != nil {
		t.Fatal(err)
	}
}

// seedFS наполняет ФС окружения файлами задания.
func seedFS(t *testing.T, e *env) {
	t.Helper()
	fs := e.deps.FS
	for p, c := range map[string]string{
		"/data/a.txt": "aaa\n",
		"/data/b.txt": "bbb\n",
	} {
		if err := fs.MkdirAll(strings.TrimSuffix(p, "/"+filepath.Base(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		wc, err := fs.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wc.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
		if err := wc.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJobsList(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	out, err := outOf(t, e, "jobs", "list")
	if err != nil || !strings.Contains(out, "media") || !strings.Contains(out, "mirror") {
		t.Fatalf("list: %v %q", err, out)
	}
}

func TestJobsAddRemoveRoundTrip(t *testing.T) {
	e := newEnv(t, "", nil)
	out, err := outOf(t, e, "jobs", "add", "photos", "--paths", "/p1,/p2", "--mode", "append", "--desc", "фото")
	if err != nil || !strings.Contains(out, "photos") {
		t.Fatalf("add: %v %q", err, out)
	}
	// Свежее окружение на том же конфиге: задание видно с диска.
	fresh := envAt(t, e.cfgDir, nil)
	out, err = outOf(t, fresh, "jobs", "list")
	if err != nil || !strings.Contains(out, "photos") {
		t.Fatalf("list после add: %v %q", err, out)
	}
	fresh2 := envAt(t, e.cfgDir, nil)
	if _, err := outOf(t, fresh2, "jobs", "remove", "photos"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	fresh3 := envAt(t, e.cfgDir, nil)
	out, err = outOf(t, fresh3, "jobs", "list")
	if err != nil || strings.Contains(out, "photos") {
		t.Fatalf("list после remove: %v %q", err, out)
	}
}

func TestJobsAdd_DuplicateAndInvalid(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	if _, err := outOf(t, e, "jobs", "add", "media", "--paths", "/x"); err == nil {
		t.Fatal("дубликат принят")
	}
	e2 := newEnv(t, "", nil)
	if _, err := outOf(t, e2, "jobs", "add", "x", "--paths", "/x", "--mode", "нет"); err == nil {
		t.Fatal("невалидный режим принят")
	}
}

func TestJobsRemove_Missing(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	if _, err := outOf(t, e, "jobs", "remove", "нет-такого"); err == nil {
		t.Fatal("remove несуществующего = nil")
	}
}

func TestBackup_RunAndDryRun(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	registerTape(t, e.cat)
	seedFS(t, e)

	out, err := outOf(t, e, "backup", "media", "--dry-run")
	if err != nil || !strings.Contains(out, "dry-run") {
		t.Fatalf("dry-run: %v %q", err, out)
	}

	e2 := envAt(t, e.cfgDir, nil)
	registerTape(t, e2.cat)
	seedFS(t, e2)
	out, err = outOf(t, e2, "backup", "media")
	if err != nil || !strings.Contains(out, "сессия #1") {
		t.Fatalf("backup: %v %q", err, out)
	}
	if len(e2.codec.WroteHeaders) != 1 || e2.codec.WroteHeaders[0].JobName != "media" {
		t.Fatalf("сессия не записана: %+v", e2.codec.WroteHeaders)
	}
	sessions, _ := e2.cat.ListSessions(context.Background(), "")
	if len(sessions) != 1 {
		t.Fatalf("каталог: %v", sessions)
	}
}

func TestBackup_UnknownJob(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	registerTape(t, e.cat)
	if _, err := outOf(t, e, "backup", "нет-такого"); err == nil {
		t.Fatal("backup неизвестного задания = nil")
	}
}

func TestTapeFormat(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	out, err := outOf(t, e, "tape", "format", "НоваяЛента", "--force")
	if err != nil || !strings.Contains(out, "НоваяЛента") {
		t.Fatalf("format: %v %q", err, out)
	}
	tapes, err := e.cat.ListTapes(context.Background())
	if err != nil || len(tapes) != 1 || tapes[0].Name != "НоваяЛента" {
		t.Fatalf("каталог после format: %v %v", tapes, err)
	}
}

func TestTapeFormat_AlreadyFormatted(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	if _, err := outOf(t, e, "tape", "format", "X"); err == nil {
		t.Fatal("format без force на отформатированной ленте = nil")
	}
}

func TestTapeReadtest(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	e.codec.Queue = [][]domain.FileMeta{{{Path: "/data/a", State: domain.StateAdded, Size: 1}}}
	out, err := outOf(t, e, "tape", "readtest")
	if err != nil || !strings.Contains(out, "проверено сессий: 1") {
		t.Fatalf("readtest: %v %q", err, out)
	}
}

func TestDaemonCommands_ViaClient(t *testing.T) {
	client := &fakeClient{
		tapes:    []port.TapeRecord{{UUID: "u1", Name: "T1", FormattedAt: 1700000000}},
		sessions: []domain.Session{{ID: 7, TapeUUID: "u1", Num: 1, Type: domain.SessionFull, Timestamp: 1700000100, JobRunID: "r1"}},
		files:    []domain.FileMeta{{Path: "/data/a.txt", Size: 10, State: domain.StateAdded}},
		copies:   []port.FileCopy{{Meta: domain.FileMeta{Path: "/data/a.txt"}, SessionID: 7, SessionNum: 1, TapeUUID: "u1"}},
		pruned:   3,
	}

	cases := []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{[]string{"tape", "info"}, "T1", false},
		{[]string{"tape", "eject"}, "извлечена", false},
		{[]string{"catalog", "tapes"}, "T1", false},
		{[]string{"catalog", "sessions"}, "u1", false},
		{[]string{"catalog", "sessions", "--tape", "u1"}, "7", false},
		{[]string{"catalog", "files", "--session", "7"}, "/data/a.txt", false},
		{[]string{"catalog", "search", "a.txt"}, "сессия 7", false},
		{[]string{"catalog", "rm", "--session", "7"}, "удалена", false},
		{[]string{"catalog", "prune", "--days", "30"}, "3", false},
		{[]string{"catalog", "files"}, "", true},
		{[]string{"catalog", "prune", "--days", "0"}, "", true},
		{[]string{"catalog", "search"}, "", true},
	}
	for _, tc := range cases {
		e := newEnv(t, "", client)
		out, err := outOf(t, e, tc.args...)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%v: ожидалась ошибка", tc.args)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: %v", tc.args, err)
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v: вывод %q не содержит %q", tc.args, out, tc.want)
		}
	}
}

func TestDaemonCommand_Stub(t *testing.T) {
	e := newEnv(t, "", nil)
	if _, err := outOf(t, e, "daemon"); err != nil {
		t.Fatalf("daemon: %v", err)
	}
}

func TestPasswd(t *testing.T) {
	e := newEnv(t, "", nil)
	e.deps.Stdin = strings.NewReader("secret\nsecret\n")
	out, err := outOf(t, e, "passwd")
	if err != nil || !strings.Contains(out, `web_password_hash = "$2`) {
		t.Fatalf("passwd: %v %q", err, out)
	}

	e2 := newEnv(t, "", nil)
	e2.deps.Stdin = strings.NewReader("a\nb\n")
	if _, err := outOf(t, e2, "passwd"); err == nil {
		t.Error("несовпадающие пароли = nil")
	}
}

func TestRestore_FullAndSmart(t *testing.T) {
	e := newEnv(t, jobTOML, nil)
	registerTape(t, e.cat)
	e.codec.Queue = [][]domain.FileMeta{{{Path: "/data/a", State: domain.StateAdded, Size: 1}}}
	out, err := outOf(t, e, "restore")
	if err != nil || !strings.Contains(out, "восстановлено файлов 1") {
		t.Fatalf("restore full: %v %q", err, out)
	}

	e2 := newEnv(t, jobTOML, nil)
	registerTape(t, e2.cat)
	sessID, err := e2.cat.CreateSession(context.Background(), domain.Session{
		TapeUUID: tapeUUID, Num: 1, Type: domain.SessionFull, Timestamp: 2, JobRunID: "r",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.cat.SaveFiles(context.Background(), sessID, []domain.FileMeta{
		{Path: "/data/a", Size: 1, Hash: "0000000000000000", State: domain.StateAdded},
	}); err != nil {
		t.Fatal(err)
	}
	e2.codec.ReadFiles = []domain.FileMeta{{Path: "/data/a", State: domain.StateAdded, Size: 1}}
	if _, err := outOf(t, e2, "restore", "--paths", "/data/a", "--dest", "/safe"); err != nil {
		t.Fatalf("restore smart: %v", err)
	}
}

func TestVersionAndHelp(t *testing.T) {
	e := newEnv(t, "", nil)
	out, err := outOf(t, e, "--version")
	if err != nil || !strings.Contains(out, "test") {
		t.Fatalf("version: %v %q", err, out)
	}
	e2 := newEnv(t, "", nil)
	if err := cli.Execute(nil, e2.deps); err != nil {
		t.Fatalf("без аргументов: %v", err)
	}
}
