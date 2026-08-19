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

package scan_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/scan"
)

// sumHash — детерминированный фейковый хеш: hex от суммы байт.
func sumHash(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	sum := 0
	for _, c := range b {
		sum += int(c)
	}
	return fmt.Sprintf("%016x", sum), nil
}

func newScanner(fs port.Filesystem) *scan.Scanner {
	return scan.New(fs, testutil.HashFunc(sumHash), nil, testutil.NoopLogger())
}

func TestScan_AppendStates(t *testing.T) {
	modTime := time.Unix(1000, 5)
	cases := []struct {
		name     string
		fs       map[string]string
		before   map[string]domain.FileMeta
		wantPath string
		want     domain.FileState
		wantHash string
	}{
		{
			name:     "new file is Added",
			fs:       map[string]string{"etc/hosts": "x"},
			before:   nil,
			wantPath: "/etc/hosts",
			want:     domain.StateAdded,
			wantHash: fmt.Sprintf("%016x", 'x'),
		},
		{
			name:     "changed size is Modified",
			fs:       map[string]string{"etc/hosts": "yy"},
			before:   map[string]domain.FileMeta{"/etc/hosts": {Path: "/etc/hosts", Size: 1, ModTime: modTime.UnixNano(), Hash: "old"}},
			wantPath: "/etc/hosts",
			want:     domain.StateModified,
			wantHash: fmt.Sprintf("%016x", 'y'*2),
		},
		{
			name:   "same size and mtime is skipped",
			fs:     map[string]string{"etc/hosts": "x"},
			before: map[string]domain.FileMeta{"/etc/hosts": {Path: "/etc/hosts", Size: 1, ModTime: zeroModTime(), Hash: "h"}},
			// MapFS по умолчанию даёт файлам нулевой mtime; в результате
			// остаётся только сам каталог /etc
			wantPath: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMapFS(tc.fs)
			if tc.wantPath != "" {
				m.MapFS["etc/hosts"].ModTime = modTime
			}
			s := newScanner(m)
			job := domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}
			got, err := s.Scan(context.Background(), job, snapshotList(tc.before))
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if tc.wantPath == "" {
				if len(got) != 1 || got[0].Path != "/etc" || !got[0].IsDir {
					t.Fatalf("только каталог корня, got %+v", got)
				}
				return
			}
			var found *domain.FileMeta
			for i := range got {
				if got[i].Path == tc.wantPath {
					found = &got[i]
				}
			}
			if found == nil {
				t.Fatalf("%q нет в результате: %+v", tc.wantPath, got)
			}
			if found.State != tc.want {
				t.Errorf("state = %s; want %s", found.State, tc.want)
			}
			if found.Hash != tc.wantHash {
				t.Errorf("hash = %q; want %q", found.Hash, tc.wantHash)
			}
		})
	}
}

// zeroModTime — UnixNano нулевого time.Time (mtime MapFS по умолчанию).
func zeroModTime() int64 {
	return time.Time{}.UnixNano()
}

func snapshotList(m map[string]domain.FileMeta) []domain.FileMeta {
	out := make([]domain.FileMeta, 0, len(m))
	for _, fm := range m {
		out = append(out, fm)
	}
	return out
}

func TestScan_AppendIgnoresDeletions(t *testing.T) {
	m := testutil.NewMapFS(map[string]string{"etc/keep": "k"})
	s := newScanner(m)
	job := domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}
	snap := []domain.FileMeta{{Path: "/etc/gone", Size: 1, State: domain.StateAdded}}

	got, err := s.Scan(context.Background(), job, snap)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, fm := range got {
		if fm.IsDeleted() {
			t.Fatalf("append не отдаёт tombstone'ы, got %+v", got)
		}
	}
}

func TestScan_MirrorEmitsTombstones(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"etc/keep": "k"})
	s := newScanner(fs)
	job := domain.Job{Name: "j", Mode: domain.ModeMirror, Paths: []string{"/etc"}}
	snap := []domain.FileMeta{
		{Path: "/etc/gone", Size: 3, Hash: "h", State: domain.StateAdded},
		{Path: "/etc/keep", Size: 1, ModTime: zeroModTime(), State: domain.StateAdded},
		{Path: "/outside/file", Size: 1, State: domain.StateAdded}, // вне корней
	}

	got, err := s.Scan(context.Background(), job, snap)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ожидался /etc (Added) и /etc/keep (Modified... нет: другой mtime нет — см. ниже), got %+v", got)
	}
	// /etc — каталог (Added), /etc/keep — mtime совпадает с MapFS-нулевым → skip;
	// значит вторая запись — tombstone /etc/gone.
	var tomb *domain.FileMeta
	for i := range got {
		if got[i].State == domain.StateDeleted {
			tomb = &got[i]
		}
	}
	if tomb == nil {
		t.Fatalf("tombstone /etc/gone не найден: %+v", got)
	}
	if tomb.Path != "/etc/gone" || tomb.Hash != "" || tomb.Size != 0 {
		t.Errorf("tombstone: %+v", tomb)
	}
}

func TestScan_TombstonesSortedByPath(t *testing.T) {
	m := testutil.NewMapFS(map[string]string{"etc/x": "1", "var/y": "1"})
	s := newScanner(m)
	job := domain.Job{Name: "j", Mode: domain.ModeMirror, Paths: []string{"/etc", "/var"}}
	snap := []domain.FileMeta{
		{Path: "/var/z", State: domain.StateAdded},
		{Path: "/etc/a", State: domain.StateAdded},
		{Path: "/etc/b", State: domain.StateAdded},
	}
	got, err := s.Scan(context.Background(), job, snap)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var tombPaths []string
	for _, fm := range got {
		if fm.IsDeleted() {
			tombPaths = append(tombPaths, fm.Path)
		}
	}
	want := []string{"/etc/a", "/etc/b", "/var/z"}
	if len(tombPaths) != len(want) {
		t.Fatalf("tombstone'ов %d; want %d", len(tombPaths), len(want))
	}
	for i := range want {
		if tombPaths[i] != want[i] {
			t.Fatalf("tombstone[%d] = %q; want %q (порядок по алфавиту)", i, tombPaths[i], want[i])
		}
	}
}

func TestScan_Exclude(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{
		"etc/keep":    "k",
		"etc/a.tmp":   "t",
		"var/cache/x": "c",
	})
	s := newScanner(fs)
	job := domain.Job{
		Name:    "j",
		Mode:    domain.ModeAppend,
		Paths:   []string{"/etc", "/var"},
		Exclude: []string{"*.tmp", "cache"},
	}
	got, err := s.Scan(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, fm := range got {
		if fm.Path == "/etc/a.tmp" || fm.Path == "/var/cache" || fm.Path == "/var/cache/x" {
			t.Errorf("исключённый путь %q попал в результат", fm.Path)
		}
	}
	var keep, etc, varDir bool
	for _, fm := range got {
		switch fm.Path {
		case "/etc/keep":
			keep = true
		case "/etc":
			etc = true
		case "/var":
			varDir = true
		}
	}
	if !keep || !etc || !varDir {
		t.Errorf("неисключённые пути потеряны: keep=%v etc=%v var=%v", keep, etc, varDir)
	}
}

func TestScan_OverlappingRootsDedupe(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"a/b/f": "x"})
	s := newScanner(fs)
	job := domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/a", "/a/b"}}
	got, err := s.Scan(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	counts := make(map[string]int)
	for _, fm := range got {
		counts[fm.Path]++
	}
	for p, n := range counts {
		if n > 1 {
			t.Errorf("путь %q встречается %d раз", p, n)
		}
	}
}

func TestScan_DirEntriesHashEmpty(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"etc/f": "x"})
	s := newScanner(fs)
	got, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var dir *domain.FileMeta
	for i := range got {
		if got[i].IsDir {
			dir = &got[i]
		}
	}
	if dir == nil {
		t.Fatal("каталог /etc не в результате")
	}
	if dir.Hash != "" {
		t.Errorf("хеш каталога = %q; want \"\"", dir.Hash)
	}
	if dir.State != domain.StateAdded || dir.Size != 0 {
		t.Errorf("каталог: %+v", dir)
	}
}

func TestScan_DirModifiedByMtime(t *testing.T) {
	m := testutil.NewMapFS(nil)
	m.MapFS["etc"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755, ModTime: time.Unix(2000, 0)}
	s := newScanner(m)
	snap := []domain.FileMeta{{Path: "/etc", ModTime: time.Unix(1000, 0).UnixNano(), IsDir: true, State: domain.StateAdded}}
	got, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, snap)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].State != domain.StateModified {
		t.Fatalf("каталог с изменившимся mtime: %+v", got)
	}
}

func TestScan_CanceledContext(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"etc/f": "x"})
	s := newScanner(fs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Scan(ctx, domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan: %v; want context.Canceled", err)
	}
}

func TestScan_WalkError(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"etc/f": "x"})
	s := newScanner(fs)
	_, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/missing"}}, nil)
	if err == nil {
		t.Fatal("несуществующий корень: ожидалась ошибка")
	}
}

func TestScan_HashErrorPropagates(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"etc/f": "x"})
	boom := errors.New("boom")
	s := scan.New(fs, testutil.HashFunc(func(io.Reader) (string, error) { return "", boom }), nil, testutil.NoopLogger())
	_, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("Scan: %v; want boom", err)
	}
}

func TestScan_OpenErrorPropagates(t *testing.T) {
	s := scan.New(&brokenFS{openErr: errors.New("open failed")}, testutil.HashFunc(sumHash), nil, testutil.NoopLogger())
	_, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, nil)
	if err == nil {
		t.Fatal("ошибка открытия файла должна пробрасываться")
	}
}

func TestScan_InvalidJob(t *testing.T) {
	s := newScanner(testutil.NewMapFS(nil))
	cases := []domain.Job{
		{Name: "", Mode: domain.ModeAppend, Paths: []string{"/etc"}},
		{Name: "j", Mode: "bogus", Paths: []string{"/etc"}},
		{Name: "j", Mode: domain.ModeAppend},
	}
	for _, job := range cases {
		if _, err := s.Scan(context.Background(), job, nil); err == nil {
			t.Errorf("job %+v должен быть отвергнут", job)
		}
	}
}

func TestScan_ProgressUpdates(t *testing.T) {
	rec := &progressRecorder{}
	fs := testutil.NewMapFS(map[string]string{"etc/f": "ab"})
	s := scan.New(fs, testutil.HashFunc(sumHash), rec, testutil.NoopLogger())
	if _, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, nil); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rec.updates) == 0 {
		t.Fatal("нет обновлений прогресса")
	}
	var sawFile bool
	for _, u := range rec.updates {
		if u.Phase != port.PhaseScan {
			t.Errorf("фаза = %q; want scan", u.Phase)
		}
		if u.CurrentFile == "/etc/f" {
			sawFile = true
		}
	}
	if !sawFile {
		t.Error("нет обновления с CurrentFile=/etc/f")
	}
}

func TestScan_SymlinkDanglingAndHardlinkPair(t *testing.T) {
	m := testutil.NewMapFS(map[string]string{"etc/first": "payload", "etc/second": "payload"})
	m.AddSymlink("/etc/dangling", "missing-target")
	m.SetLinkID("/etc/first", "dev:ino")
	m.SetLinkID("/etc/second", "dev:ino")
	got, err := newScanner(m).Scan(context.Background(), domain.Job{
		Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]domain.FileMeta, len(got))
	for _, fm := range got {
		byPath[fm.Path] = fm
	}
	if fm := byPath["/etc/dangling"]; !fm.IsSymlink() || fm.Linkname != "missing-target" || fm.Hash != "" || fm.Size != int64(len("missing-target")) {
		t.Errorf("dangling symlink = %+v", fm)
	}
	if fm := byPath["/etc/first"]; fm.IsHardlink() || fm.Hash == "" {
		t.Errorf("first hardlink = %+v, want regular content", fm)
	}
	if fm := byPath["/etc/second"]; !fm.IsHardlink() || fm.Linkname != "/etc/first" || fm.Hash != "" {
		t.Errorf("second hardlink = %+v", fm)
	}
}

func TestScan_SymlinkMtimeIsCompared(t *testing.T) {
	m := testutil.NewMapFS(nil)
	m.AddSymlink("/etc/link", "target")
	m.MapFS["etc/link"].ModTime = time.Unix(2000, 0)
	snap := []domain.FileMeta{{Path: "/etc/link", Type: domain.FileTypeSymlink, Linkname: "target", Size: 6, ModTime: time.Unix(1000, 0).UnixNano(), State: domain.StateAdded}}
	got, err := newScanner(m).Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeMirror, Paths: []string{"/etc"}}, snap)
	if err != nil {
		t.Fatal(err)
	}
	var link *domain.FileMeta
	for i := range got {
		if got[i].Path == "/etc/link" {
			link = &got[i]
		}
	}
	if link == nil || link.State != domain.StateModified {
		t.Fatalf("symlink mtime change: %+v", got)
	}
}

func TestScan_SkipsSpecialEntriesAndCountsThem(t *testing.T) {
	m := testutil.NewMapFS(map[string]string{"etc/regular": "x"})
	m.MapFS["etc/fifo"] = &fstest.MapFile{Mode: fs.ModeNamedPipe | 0o644}
	s := newScanner(m)
	got, err := s.Scan(context.Background(), domain.Job{Name: "j", Mode: domain.ModeAppend, Paths: []string{"/etc"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fm := range got {
		if fm.Path == "/etc/fifo" {
			t.Fatalf("special entry was scanned: %+v", fm)
		}
	}
	if s.SkippedSpecials() != 1 {
		t.Fatalf("SkippedSpecials = %d, want 1", s.SkippedSpecials())
	}
}

// progressRecorder — собирает все обновления.
type progressRecorder struct {
	updates []port.ProgressUpdate
}

func (p *progressRecorder) Update(u port.ProgressUpdate) { p.updates = append(p.updates, u) }
func (p *progressRecorder) Done()                        {}
func (p *progressRecorder) Fail(error)                   {}

// brokenFS — Filesystem, чей Open всегда падает; Walk/Stat работают.
type brokenFS struct {
	testutil.MapFS
	openErr error
}

func (b *brokenFS) Open(p string) (io.ReadCloser, error) { return nil, b.openErr }
