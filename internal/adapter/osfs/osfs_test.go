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

func TestWalk_SymlinkIsNotDereferenced(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var got port.Entry
	if err := osfs.New().Walk(context.Background(), root, func(p string, info port.Entry) error {
		if p == link {
			got = info
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Mode()&os.ModeSymlink == 0 || got.IsDir() {
		t.Fatalf("symlink entry = %#v, want lstat symlink", got)
	}
	if _, err := osfs.New().Stat(link); err != nil {
		t.Fatal(err)
	}
	name, err := osfs.New().Readlink(link)
	if err != nil || name != "target" {
		t.Fatalf("Readlink = %q, %v", name, err)
	}
}

// TestOSFS_HardlinkRoundTrip — жёсткие ссылки работают на всех
// поддерживаемых платформах (NTFS без особых привилегий); LinkID
// владельца и второй ссылки совпадает (Linux: inode; платформы без
// экспонирования inode: тот же пустой идентификатор).
func TestOSFS_HardlinkRoundTrip(t *testing.T) {
	root := t.TempDir()
	f := osfs.New()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Link(first, second); err != nil {
		t.Skipf("hardlink unavailable: %v", err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil || !os.SameFile(firstInfo, secondInfo) {
		t.Fatalf("hardlink identity: err=%v", err)
	}
	// LinkID через Walk: у пары хардлинков идентификатор совпадает
	ids := map[string]string{}
	err = f.Walk(context.Background(), root, func(p string, info port.Entry) error {
		ids[filepath.Base(p)] = info.LinkID()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids["first"] != ids["second"] {
		t.Errorf("LinkID пары хардлинков: %q vs %q", ids["first"], ids["second"])
	}

	// Link поверх существующего newname: файл заменяется ссылкой
	if err := f.Link(first, second); err != nil {
		t.Fatalf("Link поверх существующего: %v", err)
	}
}

// TestOSFS_SymlinkRoundTrip — симлинки: dangling-цель читается,
// Symlink поверх существующего файла заменяет его; платформы без
// привилегий на симлинки пропускают тест.
func TestOSFS_SymlinkRoundTrip(t *testing.T) {
	root := t.TempDir()
	f := osfs.New()
	link := filepath.Join(root, "link")
	if err := f.Symlink("missing", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, err := f.Readlink(link); err != nil || got != "missing" {
		t.Fatalf("dangling Readlink = %q, %v", got, err)
	}

	// Symlink поверх существующего файла: remove + create
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Symlink("missing", file); err != nil {
		t.Fatalf("Symlink поверх файла: %v", err)
	}
	if got, err := f.Readlink(file); err != nil || got != "missing" {
		t.Fatalf("Readlink после замены = %q, %v", got, err)
	}

	// Symlink на путь непустого каталога: remove непустого каталога
	// падает — ошибка возвращается, каталог не тронут
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "inner"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Symlink("x", sub); err == nil {
		t.Error("Symlink поверх непустого каталога: нет ошибки")
	}
}

// TestReadlink_NotALink — readlink обычного файла — ошибка.
func TestReadlink_NotALink(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plain")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := osfs.New().Readlink(file); err == nil {
		t.Error("Readlink обычного файла: нет ошибки")
	}
}

// TestMkdirAll_OverFileIsError — MkdirAll по пути существующего файла
// — ошибка (не молчаливый успех).
func TestMkdirAll_OverFileIsError(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plain")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := osfs.New().MkdirAll(file, 0o755); err == nil {
		t.Error("MkdirAll поверх файла: нет ошибки")
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

// TestLink_MissingOldname — Link на несуществующий владелец — ошибка.
func TestLink_MissingOldname(t *testing.T) {
	root := t.TempDir()
	if err := osfs.New().Link(
		filepath.Join(root, "no-such"),
		filepath.Join(root, "second"),
	); err == nil {
		t.Error("Link с несуществующим oldname: нет ошибки")
	}
}
