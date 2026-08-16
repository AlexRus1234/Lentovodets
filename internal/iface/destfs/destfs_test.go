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
