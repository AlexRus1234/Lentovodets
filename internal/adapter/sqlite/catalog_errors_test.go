package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lentovodec/internal/adapter/sqlite"
)

// canceledCtx возвращает уже отменённый контекст.
func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNew_NotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(path, []byte("это не SQLite-файл"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := sqlite.New(path)
	if err == nil {
		t.Fatal("New на битом файле = nil, want ошибка (PRAGMA/схема не применяются)")
	}
	if !strings.Contains(err.Error(), "sqlite:") {
		t.Errorf("err = %v, want префикс sqlite:", err)
	}
}

func TestGetTapeByUUID_CanceledContext(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := canceledCtx()
	if _, err := c.GetTapeByUUID(ctx, "tape-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestLastSessionNum_CanceledContext(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := canceledCtx()
	if _, err := c.LastSessionNum(ctx, "tape-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestListTapes_CanceledContext(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := canceledCtx()
	if _, err := c.ListTapes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestListSessions_CanceledContext(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := canceledCtx()
	for _, filter := range []string{"", "tape-1"} {
		if _, err := c.ListSessions(ctx, filter); !errors.Is(err, context.Canceled) {
			t.Fatalf("ListSessions(%q): err = %v, want context.Canceled", filter, err)
		}
	}
}

func TestGetFilesBySession_CanceledContext(t *testing.T) {
	c, sess := newCatalog(t)
	id := mustSession(t, c, sess)
	ctx := canceledCtx()
	if _, err := c.GetFilesBySession(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestGetLatestFileStates_CanceledContext(t *testing.T) {
	c, sess := newCatalog(t)
	mustSession(t, c, sess)
	ctx := canceledCtx()
	if _, err := c.GetLatestFileStates(ctx, []string{"/x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSearchFiles_CanceledContext(t *testing.T) {
	c, sess := newCatalog(t)
	mustSession(t, c, sess)
	ctx := canceledCtx()
	if _, err := c.SearchFiles(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestGetAllFileCopies_CanceledContext(t *testing.T) {
	c, sess := newCatalog(t)
	mustSession(t, c, sess)
	ctx := canceledCtx()
	if _, err := c.GetAllFileCopies(ctx, "/x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDeleteSession_CanceledContext(t *testing.T) {
	c, sess := newCatalog(t)
	id := mustSession(t, c, sess)
	ctx := canceledCtx()
	if err := c.DeleteSession(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestPruneSessions_CanceledContext(t *testing.T) {
	c, _ := newCatalog(t)
	ctx := canceledCtx()
	if _, err := c.PruneSessions(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
