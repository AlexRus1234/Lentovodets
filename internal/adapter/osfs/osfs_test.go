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

package osfs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"lentovodec/internal/adapter/osfs"
	"lentovodec/internal/port"
)

// newTestTree создаёт во временном каталоге дерево:
//
//	root/
//	├── a.txt ("alpha")
//	└── sub/
//	    └── b.txt ("beta")
func newTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWalk_VisitsAllEntries(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()

	var paths []string
	err := f.Walk(context.Background(), root, func(p string, _ port.Entry) error {
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{root, filepath.Join(root, "a.txt"), filepath.Join(root, "sub"), filepath.Join(root, "sub", "b.txt")}
	sort.Strings(paths)
	sort.Strings(want)
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Errorf("Walk обошёл %v, want %v", paths, want)
	}
}

func TestWalk_PassesEntryDetails(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()

	seen := map[string]port.Entry{}
	err := f.Walk(context.Background(), root, func(p string, info port.Entry) error {
		seen[p] = info
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	dir := seen[root]
	if !dir.IsDir() {
		t.Errorf("корень: IsDir = false, want true")
	}
	file := seen[filepath.Join(root, "a.txt")]
	if file.IsDir() {
		t.Errorf("a.txt: IsDir = true, want false")
	}
	if file.Name() != "a.txt" {
		t.Errorf("a.txt: Name() = %q, want %q", file.Name(), "a.txt")
	}
	if file.Size() != int64(len("alpha")) {
		t.Errorf("a.txt: Size() = %d, want %d", file.Size(), len("alpha"))
	}
	if file.ModTime().IsZero() {
		t.Error("a.txt: ModTime() нулевой")
	}
	if file.Mode().Perm() == 0 {
		t.Error("a.txt: Mode().Perm() пустой")
	}
}

func TestWalk_CallbackErrorStopsWalk(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()

	sentinel := errors.New("стоп")
	var visited int
	err := f.Walk(context.Background(), root, func(_ string, _ port.Entry) error {
		visited++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel (обёрнутый)", err)
	}
	if visited != 1 {
		t.Errorf("visited = %d, want 1 (обход остановился на первом элементе)", visited)
	}
}

func TestWalk_CanceledContext(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := f.Walk(ctx, root, func(_ string, _ port.Entry) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWalk_CancelMidWalk(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()
	ctx, cancel := context.WithCancel(context.Background())

	var visited int
	err := f.Walk(ctx, root, func(_ string, _ port.Entry) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if visited != 1 {
		t.Errorf("visited = %d, want 1", visited)
	}
}

func TestWalk_RootNotFound(t *testing.T) {
	f := osfs.New()
	missing := filepath.Join(t.TempDir(), "нет-такого")
	err := f.Walk(context.Background(), missing, func(_ string, _ port.Entry) error { return nil })
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestOpen_ReadsFile(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()

	rc, err := f.Open(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	buf := make([]byte, len("alpha")+1)
	n, err := rc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "alpha" {
		t.Errorf("прочитано %q, want %q", buf[:n], "alpha")
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpen_MissingFile(t *testing.T) {
	f := osfs.New()
	_, err := f.Open(filepath.Join(t.TempDir(), "нет.txt"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestStat(t *testing.T) {
	root := newTestTree(t)
	f := osfs.New()

	info, err := f.Stat(filepath.Join(root, "sub"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() || info.Name() != "sub" {
		t.Errorf("Stat(sub) = {%s isDir=%v}, want каталог sub", info.Name(), info.IsDir())
	}
}

func TestStat_MissingPath(t *testing.T) {
	f := osfs.New()
	_, err := f.Stat(filepath.Join(t.TempDir(), "нет"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestReadDir_SortsDirectoriesThenNames(t *testing.T) {
	root := newTestTree(t)
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := osfs.New()
	entries, err := f.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	got := []string{entries[0].Name, entries[1].Name, entries[2].Name}
	want := []string{"sub", "a.txt", "z.txt"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if entries[0].ModTime.IsZero() {
		t.Errorf("directory details = %+v", entries[0])
	}
}

func TestReadDir_DoesNotFilterHiddenFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := osfs.New().ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name != ".hidden" {
		t.Fatalf("ReadDir hidden = %+v, %v", entries, err)
	}
}

func TestReadDir_Errors(t *testing.T) {
	f := osfs.New()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := f.ReadDir(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing error = %v, want fs.ErrNotExist", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadDir(file); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("file error = %v, want syscall.ENOTDIR", err)
	}
}

func TestMkdirAll_CreatesParents(t *testing.T) {
	f := osfs.New()
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := f.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	info, err := f.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("после MkdirAll Stat(%q) = (%v, %v), want каталог", dir, info, err)
	}
}

func TestCreate_WriteAndReadback(t *testing.T) {
	f := osfs.New()
	path := filepath.Join(t.TempDir(), "out.txt")

	wc, err := f.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := wc.Write([]byte("данные")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := f.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != int64(len("данные")) {
		t.Errorf("Size() = %d, want %d", info.Size(), len("данные"))
	}
}

func TestCreate_TruncatesExisting(t *testing.T) {
	f := osfs.New()
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("старое-длинное-содержимое"), 0o644); err != nil {
		t.Fatal(err)
	}
	wc, err := f.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := f.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("Size() после Create = %d, want 0", info.Size())
	}
}

func TestCreate_MissingDirectory(t *testing.T) {
	f := osfs.New()
	_, err := f.Create(filepath.Join(t.TempDir(), "нет-каталога", "f.txt"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestRemove_File(t *testing.T) {
	f := osfs.New()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := f.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("после Remove Stat = %v, want fs.ErrNotExist", err)
	}
}

func TestRemove_EmptyDir(t *testing.T) {
	f := osfs.New()
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(dir); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestRemove_Missing(t *testing.T) {
	f := osfs.New()
	err := f.Remove(filepath.Join(t.TempDir(), "нет"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}
