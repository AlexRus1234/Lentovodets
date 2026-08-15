package testutil_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

func TestMapFS_ReadPath(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{
		"/etc/hosts": "127.0.0.1 localhost\n",
		"/etc/conf":  "value=1\n",
	})

	r, err := fs.Open("/etc/hosts")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if string(data) != "127.0.0.1 localhost\n" {
		t.Errorf("прочитано %q", data)
	}

	info, err := fs.Stat("/etc/conf")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 8 || info.IsDir() {
		t.Errorf("Stat /etc/conf: size=%d isDir=%v", info.Size(), info.IsDir())
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 644", perm)
	}

	if _, err := fs.Stat("/missing"); err == nil {
		t.Error("Stat отсутствующего: want error")
	}
	if _, err := fs.Open("/missing"); err == nil {
		t.Error("Open отсутствующего: want error")
	}
}

func TestMapFS_WritePath(t *testing.T) {
	fs := testutil.NewMapFS(nil)

	if err := fs.MkdirAll("/dest/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	w, err := fs.Create("/dest/sub/file.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := w.Write([]byte("новое содержимое")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := fs.Open("/dest/sub/file.txt")
	if err != nil {
		t.Fatalf("Open после Create: %v", err)
	}
	data, _ := io.ReadAll(r)
	_ = r.Close()
	if string(data) != "новое содержимое" {
		t.Errorf("прочитано %q", data)
	}

	// Повторный Create усекает.
	w2, _ := fs.Create("/dest/sub/file.txt")
	_, _ = w2.Write([]byte("short"))
	_ = w2.Close()
	r2, _ := fs.Open("/dest/sub/file.txt")
	data2, _ := io.ReadAll(r2)
	_ = r2.Close()
	if string(data2) != "short" {
		t.Errorf("после усечения: %q", data2)
	}

	if err := fs.Remove("/dest/sub/file.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := fs.Open("/dest/sub/file.txt"); err == nil {
		t.Error("Open после Remove: want error")
	}
	if err := fs.Remove("/nope"); err == nil {
		t.Error("Remove отсутствующего: want error")
	}
}

func TestMapFS_Walk(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{
		"/tank/a.txt":     "a",
		"/tank/sub/b.txt": "b",
	})

	var visited []string
	err := fs.Walk(context.Background(), "/tank", func(p string, info port.Entry) error {
		visited = append(visited, p)
		if !strings.HasPrefix(p, "/tank") {
			t.Errorf("путь %q вне корня", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	joined := strings.Join(visited, ",")
	for _, want := range []string{"/tank", "/tank/a.txt", "/tank/sub", "/tank/sub/b.txt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Walk не посетил %q (было: %v)", want, visited)
		}
	}

	stop := errors.New("stop")
	err = fs.Walk(context.Background(), "/tank", func(p string, _ port.Entry) error {
		return stop
	})
	if !errors.Is(err, stop) {
		t.Errorf("ошибка из fn: %v, want stop", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fs.Walk(canceled, "/tank", func(string, port.Entry) error { return nil }); err == nil {
		t.Error("Walk с отменённым ctx: want error")
	}

	if err := fs.Walk(context.Background(), "/missing", func(string, port.Entry) error { return nil }); err == nil {
		t.Error("Walk отсутствующего корня: want error")
	}
}
