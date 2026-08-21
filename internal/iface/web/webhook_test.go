package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"lentovodec/internal/iface/web"
	"lentovodec/internal/testutil"
)

func TestWebhookNotifier_PostsJSONAndRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		var got web.TaskFinishSnapshot
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		if got.Event != "task_finished" || got.State != "success" {
			t.Errorf("snapshot = %+v", got)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	notify, err := web.NewWebhookNotifier(server.URL, time.Second, testutil.NoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	notify(web.TaskFinishSnapshot{Event: "task_finished", State: "success"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestWebhookNotifier_PerAttemptTimeoutAllowsRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(100 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notify, err := web.NewWebhookNotifier(server.URL, 30*time.Millisecond, testutil.NoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	notify(web.TaskFinishSnapshot{Event: "task_finished"})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("requests = %d, want retry after per-attempt timeout", calls.Load())
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("delivery took %v, want bounded by per-attempt timeouts", elapsed)
	}
}

func TestWebhookNotifier_DoesNotFollowRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	var redirectCalls atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCalls.Add(1)
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	notify, err := web.NewWebhookNotifier(redirect.URL, time.Second, testutil.NoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	notify(web.TaskFinishSnapshot{Event: "task_finished"})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && redirectCalls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	if targetCalls.Load() != 0 || redirectCalls.Load() != 2 {
		t.Fatalf("redirect requests = %d, target requests = %d", redirectCalls.Load(), targetCalls.Load())
	}
}

func TestTaskRegistry_FinishCallbackSnapshot(t *testing.T) {
	finished := make(chan web.TaskFinishSnapshot, 1)
	registry := web.NewTaskRegistryWithFinish("1.2.3", func(snapshot web.TaskFinishSnapshot) {
		finished <- snapshot
	})
	task := registry.Start("task-test", "backup", func(task *web.Task) {
		task.SetResult("media", 123, 4, []string{"LTO-001"})
		web.NewTaskProgress(task, testutil.FixedClock(time.Unix(1700000000, 0))).Done()
	})
	if err := registry.WaitAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		if got.TaskID != task.ID || got.Job != "media" || got.Bytes != 123 || got.Files != 4 || got.Version != "1.2.3" {
			t.Errorf("snapshot = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("finish callback was not called")
	}
}

func TestTaskRegistry_FinishErrorCallbackIncludesError(t *testing.T) {
	finished := make(chan web.TaskFinishSnapshot, 1)
	registry := web.NewTaskRegistryWithFinish("test", func(snapshot web.TaskFinishSnapshot) {
		finished <- snapshot
	})
	registry.Start("task-error", "restore", func(task *web.Task) {
		web.NewTaskProgress(task, testutil.FixedClock(time.Unix(1700000000, 0))).Fail(context.Canceled)
	})
	if err := registry.WaitAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		if got.State != "error" || got.Error != context.Canceled.Error() {
			t.Errorf("snapshot = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("finish callback was not called")
	}
}
