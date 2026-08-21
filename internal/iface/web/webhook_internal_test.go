package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"lentovodec/internal/port"
)

func TestTaskAwaitingShutdown_FinishCallback(t *testing.T) {
	finished := make(chan TaskFinishSnapshot, 1)
	registry := NewTaskRegistryWithFinish("test", func(snapshot TaskFinishSnapshot) {
		finished <- snapshot
	})
	registry.Start("task-awaiting", "backup", func(task *Task) {
		task.update(testProgressUpdate(800), time.Unix(1700000001, 0))
		if _, ok := task.enterAwaiting("вставьте кассету", "LTO-002"); !ok {
			t.Fatal("enterAwaiting failed")
		}
		task.finishError(errors.New("context canceled"), time.Unix(1700000002, 0))
	})
	if err := registry.WaitAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := <-finished
	if got.State != taskError || got.Error != "context canceled" || got.Bytes != 800 {
		t.Fatalf("snapshot = %+v", got)
	}
	if got.Tapes == nil {
		t.Fatal("tapes must be an empty array, not null")
	}
}

func testProgressUpdate(bytes int64) port.ProgressUpdate {
	return port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: bytes, TotalBytes: 1000}
}
