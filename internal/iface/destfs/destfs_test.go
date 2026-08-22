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

package destfs_test

import (
	"context"
	"io"
	"testing"

	"lentovodec/internal/iface/destfs"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// writeAll — утилита записи содержимого через FileWriter.
func writeAll(t *testing.T, w io.WriteCloser, data string) {
	t.Helper()
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatalf("запись: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("закрытие: %v", err)
	}
}

func TestWrap_WritesGoUnderDest(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if err := fs.MkdirAll("/etc/nginx", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	wc, err := fs.Create("/etc/nginx/nginx.conf")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeAll(t, wc, "daemon off;\n")

	rc, err := inner.Open("/safe/etc/nginx/nginx.conf")
	if err != nil {
		t.Fatalf("файл не появился под dest: %v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "daemon off;\n" {
		t.Errorf("содержимое = %q", body)
	}
}

func TestWrap_RemoveRelocatesTombstones(t *testing.T) {
	inner := testutil.NewMapFS(map[string]string{"/safe/old.txt": "x"})
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if err := fs.Remove("/old.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := inner.Stat("/safe/old.txt"); err == nil {
		t.Error("tombstone не удалил файл под dest")
	}
}

func TestWrap_PassthroughReadAndWalk(t *testing.T) {
	inner := testutil.NewMapFS(map[string]string{"/data/a.txt": "a"})
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if _, err := fs.Stat("/data/a.txt"); err != nil {
		t.Errorf("Stat должен проходить в inner без изменений: %v", err)
	}
	var seen int
	err := fs.Walk(context.Background(), "/data", func(p string, _ port.Entry) error {
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if seen == 0 {
		t.Error("Walk ничего не обошёл")
	}
}

func TestWrap_EmptyDestReturnsInner(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	if destfs.Wrap(inner, "") == nil {
		t.Fatal("Wrap с пустым dest вернул nil")
	}
}

func TestWrap_VolumePathStripped(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if err := fs.MkdirAll("C:/data", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	wc, err := fs.Create("C:/data/f.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeAll(t, wc, "x")
	if _, err := inner.Stat("/safe/data/f.txt"); err != nil {
		t.Errorf("файл с именем тома должен лечь без тома: %v", err)
	}
}

// TestWrap_RejectsTraversal — пути индекса с ".." отклоняются на всех
// операциях записи: индекс ленты может быть повреждён или зловреден.
func TestWrap_RejectsTraversal(t *testing.T) {
	inner := testutil.NewMapFS(map[string]string{"/safe/comp.txt": "x"})
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if _, err := fs.Create("../../evil.txt"); err == nil {
		t.Error("Create: путь с ../.. должен отклоняться")
	}
	if err := fs.MkdirAll("../escape", 0o755); err == nil {
		t.Error("MkdirAll: путь с .. должен отклоняться")
	}
	if err := fs.Remove("../../../etc/passwd"); err == nil {
		t.Error("Remove: путь с ../.. должен отклоняться")
	}
	if err := fs.Link("/comp.txt", "../evil-link"); err == nil {
		t.Error("Link: newname с .. должен отклоняться")
	}
	if err := fs.Symlink("x", "../evil-sym"); err == nil {
		t.Error("Symlink: путь с .. должен отклоняться")
	}
	if _, err := inner.Stat("/safe/comp.txt"); err != nil {
		t.Errorf("легитимные файлы dest не пострадали: %v", err)
	}
}

// TestWrap_SymlinkEscapeBlocked — запись через восстановленный symlink
// (сама ссылка легитимна, запись по ней — побег за dest) запрещена:
// os.Create/os.Remove следуют по ссылкам.
func TestWrap_SymlinkEscapeBlocked(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if err := fs.MkdirAll("safe-dir", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// восстановили symlink с относительной целью наружу — это законно
	if err := fs.Symlink("../../etc", "safe-dir/link"); err != nil {
		t.Fatalf("Symlink (легитимная ссылка): %v", err)
	}
	// ...но писать «внутрь» него нельзя
	if _, err := fs.Create("safe-dir/link/passwd"); err == nil {
		t.Error("Create через symlink должен отклоняться")
	}
	if err := fs.MkdirAll("safe-dir/link/sub", 0o755); err == nil {
		t.Error("MkdirAll через symlink должен отклоняться")
	}
	if err := fs.Remove("safe-dir/link/passwd"); err == nil {
		t.Error("Remove через symlink должен отклоняться")
	}
	// а сама ссылка удаляется (os.Remove не следует финальной ссылке)
	if err := fs.Remove("safe-dir/link"); err != nil {
		t.Errorf("Remove самой ссылки: %v", err)
	}
}

// TestWrap_CreateOverSymlinkRejected — Create на пути, где уже лежит
// чужой symlink (грязный dest), отклоняется: os.Create усекал бы цель
// ссылки.
func TestWrap_CreateOverSymlinkRejected(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	inner.AddSymlink("/safe/evil", "../outside")
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if _, err := fs.Create("evil"); err == nil {
		t.Error("Create поверх существующего symlink должен отклоняться")
	}
}

// TestWrap_SymlinkInvalidatesDirCache — каталог, превращённый в symlink
// нашим же Symlink, выпадает из кэша проверенных каталогов.
func TestWrap_SymlinkInvalidatesDirCache(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if err := fs.MkdirAll("a/b", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	wc, err := fs.Create("a/b/f.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeAll(t, wc, "x")
	// пустой каталог b заменяется ссылкой (osfs.Symlink сначала Remove)
	if err := fs.Remove("a/b/f.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := fs.Symlink("../out", "a/b"); err != nil {
		t.Fatalf("Symlink поверх пустого каталога: %v", err)
	}
	if _, err := fs.Create("a/b/new.txt"); err == nil {
		t.Error("Create через свежий symlink-каталог должен отклоняться")
	}
}

// TestWrap_RelativeSymlinkTargetAllowed — linkname — содержимое ссылки,
// а не путь записи: легитимные относительные цели («../lib/x») не
// блокируются, ссылка восстанавливается.
func TestWrap_RelativeSymlinkTargetAllowed(t *testing.T) {
	inner := testutil.NewMapFS(nil)
	var fs port.Filesystem = destfs.Wrap(inner, "/safe")

	if err := fs.MkdirAll("app/lib", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fs.Symlink("../lib/libfoo.so.1", "app/lib/libfoo.so"); err != nil {
		t.Fatalf("Symlink с относительной целью: %v", err)
	}
	if link, err := inner.Readlink("/safe/app/lib/libfoo.so"); err != nil || link != "../lib/libfoo.so.1" {
		t.Errorf("ссылка не восстановлена: %q, %v", link, err)
	}
	// соседние файлы пишутся без помех
	if _, err := fs.Create("app/lib/real.txt"); err != nil {
		t.Errorf("Create рядом со ссылкой: %v", err)
	}
}
