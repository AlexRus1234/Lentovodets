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

// Внутренние тесты смены кассет демона: пауза awaiting_tape, ответ
// оператора, отмена (graceful shutdown), чтение ярлыка цепочки restore.

package web

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/format"
)

// changerEnv — минимальный Server для тестов changer'а: без конфига
// и HTTP, только зависимости, которых касается смена кассеты.
type changerEnv struct {
	srv  *Server
	reg  *TaskRegistry
	cat  *testutil.MemCatalog
	tape *testutil.FakeTape
}

func newChangerEnv(t *testing.T) *changerEnv {
	t.Helper()
	env := &changerEnv{
		reg:  NewTaskRegistry(),
		cat:  testutil.NewMemCatalog(),
		tape: testutil.NewFakeTape(),
	}
	env.srv = &Server{
		deps: Deps{
			Log:     testutil.NoopLogger(),
			Catalog: env.cat,
			Codec:   &testutil.FakeCodec{},
			Rand:    testutil.FixedRand("22222222-2222-4222-8222-222222222222"),
			Clock:   testutil.FixedClock(time.Unix(1700000000, 0)),
			OpenTape: func(string) (port.Tape, error) {
				return env.tape, nil
			},
		},
		tasks: env.reg,
	}
	return env
}

// runWithChanger запускает задачу реестра, вызывающую fn с changer'ом.
func (e *changerEnv) runWithChanger(t *testing.T, job string, fn func(ch *daemonChanger) error) {
	t.Helper()
	e.reg.Start("task-x", job, func(tk *Task) {
		ch := &daemonChanger{srv: e.srv, task: tk, job: job, cur: e.tape}
		defer ch.finish()
		if err := fn(ch); err != nil {
			tk.finishError(err, e.srv.deps.Clock.Now())
			return
		}
		tk.finishSuccess(e.srv.deps.Clock.Now())
	})
}

// taskOf возвращает задачу окружения.
func (e *changerEnv) taskOf(t *testing.T) *Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := e.reg.Get("task-x"); ok {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("задача task-x не зарегистрирована за 5с")
	return nil
}

// waitAwaiting ждёт состояния awaiting_tape.
func waitAwaiting(t *testing.T, task *Task) (string, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := task.Snapshot()
		if snap.State == taskAwaitingTape {
			return snap.Message, snap.SuggestedTapeName
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("задача не достигла awaiting_tape за 5с")
	return "", ""
}

// waitState ждёт состояние задачи.
func waitState(t *testing.T, task *Task, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task.Snapshot().State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("задача не достигла состояния %s за 5с", state)
}

func TestDaemonChanger_BackupFormatsAndRegisters(t *testing.T) {
	env := newChangerEnv(t)
	var gotLabel string
	env.runWithChanger(t, "backup", func(ch *daemonChanger) error {
		tape, label, err := ch.RequestNext(context.Background(), port.NextTapeRequest{
			JobName:      "media",
			FinishedTape: "test-tape",
			NextTapeName: "media-014",
			Part:         2,
			Reason:       port.ReasonSpan,
		})
		if err != nil {
			return err
		}
		gotLabel = label.Name
		return tape.Rewind(context.Background()) // лента возвращена открытой
	})

	task := env.taskOf(t)
	message, suggested := waitAwaiting(t, task)
	if !strings.Contains(message, "test-tape") || !strings.Contains(message, "media-014") ||
		!strings.Contains(message, "чистую") {
		t.Errorf("message=%q, want текст про чистую кассету media-014", message)
	}
	if suggested != "media-014" {
		t.Errorf("suggested=%q, want media-014", suggested)
	}
	// Пустое имя = предложенное.
	if name, err := task.Continue(""); err != nil || name != "media-014" {
		t.Fatalf("Continue(\"\"): %q %v, want media-014", name, err)
	}

	waitState(t, task, taskSuccess)
	if gotLabel != "media-014" {
		t.Errorf("ярлык новой кассеты: %q, want media-014", gotLabel)
	}
	tapes, err := env.cat.ListTapes(context.Background())
	if err != nil || len(tapes) != 1 || tapes[0].Name != "media-014" {
		t.Fatalf("каталог после форматирования: %v %v", tapes, err)
	}
}

func TestDaemonChanger_RestoreReadsLabelWithoutFormat(t *testing.T) {
	env := newChangerEnv(t)
	// Кассета цепочки уже записана: ярлык T2 читается, НЕ форматируется.
	label, err := format.New(env.tape, env.srv.deps.Codec, env.cat,
		env.srv.deps.Rand, env.srv.deps.Clock, env.srv.deps.Log).
		Format(context.Background(), "T2", false)
	if err != nil {
		t.Fatal(err)
	}
	uuidBefore, tapeBefore := label.UUID, env.tape.BlockCount()

	var gotName, gotUUID string
	env.runWithChanger(t, "restore", func(ch *daemonChanger) error {
		_, got, err := ch.RequestNext(context.Background(), port.NextTapeRequest{
			FinishedTape: "T1",
			NextTapeName: "T2",
			Part:         2,
			Reason:       port.ReasonRestore,
		})
		gotName, gotUUID = got.Name, got.UUID
		return err
	})

	task := env.taskOf(t)
	message, suggested := waitAwaiting(t, task)
	if !strings.Contains(message, "прочитана") || !strings.Contains(message, "T2") {
		t.Errorf("message=%q, want текст про прочитанную кассету T2", message)
	}
	if suggested != "T2" {
		t.Errorf("suggested=%q, want T2 (имя из указателя продолжения)", suggested)
	}
	if name, err := task.Continue("T2"); err != nil || name != "T2" {
		t.Fatalf("Continue: %q %v", name, err)
	}

	waitState(t, task, taskSuccess)
	if gotName != "T2" || gotUUID != uuidBefore {
		t.Errorf("ярлык вставленной кассеты: %s/%s, want T2/%s", gotName, gotUUID, uuidBefore)
	}
	if n := env.tape.BlockCount(); n != tapeBefore {
		t.Errorf("кассета перезаписана: %d блоков, было %d", n, tapeBefore)
	}
}

func TestDaemonChanger_RequestNextFailures(t *testing.T) {
	t.Run("сбой открытия устройства", func(t *testing.T) {
		env := newChangerEnv(t)
		env.srv.deps.OpenTape = func(string) (port.Tape, error) {
			return nil, errors.New("нет устройства")
		}
		env.runWithChanger(t, "backup", func(ch *daemonChanger) error {
			_, _, err := ch.RequestNext(context.Background(), port.NextTapeRequest{
				JobName: "media", FinishedTape: "T1", NextTapeName: "T2",
				Part: 2, Reason: port.ReasonSpan,
			})
			return err
		})
		task := env.taskOf(t)
		waitAwaiting(t, task)
		if _, err := task.Continue("T2"); err != nil {
			t.Fatal(err)
		}
		waitState(t, task, taskError)
		if snap := task.Snapshot(); !strings.Contains(snap.Error, "нет устройства") {
			t.Errorf("ошибка задачи: %q", snap.Error)
		}
	})

	t.Run("нечистая кассета для бекапа", func(t *testing.T) {
		env := newChangerEnv(t)
		if _, err := format.New(env.tape, env.srv.deps.Codec, env.cat,
			env.srv.deps.Rand, env.srv.deps.Clock, env.srv.deps.Log).
			Format(context.Background(), "T1", false); err != nil {
			t.Fatal(err)
		}
		env.runWithChanger(t, "backup", func(ch *daemonChanger) error {
			_, _, err := ch.RequestNext(context.Background(), port.NextTapeRequest{
				JobName: "media", FinishedTape: "T1", NextTapeName: "T2",
				Part: 2, Reason: port.ReasonEnospc,
			})
			return err
		})
		task := env.taskOf(t)
		waitAwaiting(t, task)
		if _, err := task.Continue("T2"); err != nil {
			t.Fatal(err)
		}
		waitState(t, task, taskError)
		if snap := task.Snapshot(); !strings.Contains(snap.Error, "не чиста") ||
			!strings.Contains(snap.Error, "T1") {
			t.Errorf("ошибка задачи: %q, want подсказка про нечистую кассету", snap.Error)
		}
	})

	t.Run("пустая кассета цепочки restore", func(t *testing.T) {
		env := newChangerEnv(t)
		env.runWithChanger(t, "restore", func(ch *daemonChanger) error {
			_, _, err := ch.RequestNext(context.Background(), port.NextTapeRequest{
				FinishedTape: "T1", NextTapeName: "T2",
				Part: 2, Reason: port.ReasonRestore,
			})
			return err
		})
		task := env.taskOf(t)
		waitAwaiting(t, task)
		if _, err := task.Continue(""); err != nil {
			t.Fatal(err)
		}
		waitState(t, task, taskError)
		if snap := task.Snapshot(); !strings.Contains(snap.Error, "чистая лента") &&
			!strings.Contains(snap.Error, "ярлык") {
			t.Errorf("ошибка задачи: %q, want ошибка чтения ярлыка", snap.Error)
		}
	})
}

// closeErrTape — лента, чей Close всегда падает.
type closeErrTape struct {
	port.Tape
}

func (closeErrTape) Close() error { return errors.New("close boom") }

func TestDaemonChanger_FinishLogsCloseError(t *testing.T) {
	env := newChangerEnv(t)
	ch := &daemonChanger{srv: env.srv, task: &Task{}, cur: closeErrTape{Tape: env.tape}}
	ch.finish() // сбой закрытия логируется, не паникует
}

func TestDaemonChanger_CancelWhileAwaiting(t *testing.T) {
	env := newChangerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())

	env.runWithChanger(t, "backup", func(ch *daemonChanger) error {
		_, _, err := ch.RequestNext(ctx, port.NextTapeRequest{
			JobName: "media", FinishedTape: "T1", NextTapeName: "T2", Part: 2,
			Reason: port.ReasonSpan,
		})
		return err
	})

	task := env.taskOf(t)
	waitAwaiting(t, task)
	cancel() // graceful shutdown: ожидание закрывается, задача не висит

	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_ = env.reg.WaitAll(context.Background())
	}()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAll висит после отмены ожидающей задачи")
	}
	waitState(t, task, taskError)
	if snap := task.Snapshot(); !strings.Contains(snap.Error, "отменено") {
		t.Errorf("ошибка задачи: %q, want отмена ожидания", snap.Error)
	}
}

func TestDaemonChanger_CloseTapeEjects(t *testing.T) {
	env := newChangerEnv(t)
	tape := testutil.NewFakeTape()
	ch := &daemonChanger{srv: env.srv, task: &Task{}, cur: tape}

	if err := ch.CloseTape(context.Background(), tape); err != nil {
		t.Fatalf("CloseTape: %v", err)
	}
	if !tape.Ejected() {
		t.Error("кассета не извлечена")
	}
	if ch.cur != nil {
		t.Error("после CloseTape текущая лента должна быть сброшена")
	}
	ch.finish() // закрытое уже не закрывается (аналог closeOnce CLI)
}

func TestTask_ContinueWrongState(t *testing.T) {
	env := newChangerEnv(t)
	env.reg.Start("task-y", "backup", func(tk *Task) {
		tk.finishSuccess(env.srv.deps.Clock.Now())
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := env.reg.Get("task-y"); ok && task.Snapshot().State == taskSuccess {
			// Завершённая задача не ждёт кассету.
			if _, err := task.Continue("T2"); err == nil {
				t.Error("Continue завершённой задачи: нет ошибки")
			}
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Бегущая (но не ожидающая) задача тоже не примет ответ; вход
	// в ожидание завершённой задачи отклоняется.
	running := &Task{state: taskRunning}
	if _, err := running.Continue("T2"); err == nil {
		t.Error("Continue бегущей задачи: нет ошибки")
	}
	finished := &Task{state: taskSuccess}
	if _, ok := finished.enterAwaiting("msg", "T2"); ok {
		t.Error("enterAwaiting завершённой задачи: пропущен")
	}
}
