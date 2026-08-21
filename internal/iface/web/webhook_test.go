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
