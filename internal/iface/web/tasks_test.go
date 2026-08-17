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

// Тесты фоновых задач: бекап/восстановление через API, единственная
// активная задача, прогресс, гонки в реестре.

package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	tapeformat "lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
)

// progressView — проекция прогресса для проверок.
type progressView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Phase string `json:"phase"`
	Error string `json:"error"`
}

// waitFor опрашивает прогресс задачи, пока cond не станет истинным.
func waitFor(t *testing.T, env *testEnv, id string, cond func(progressView) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		_, body := do(t, env, http.MethodGet, "/api/tasks/"+id+"/progress", "")
		last = body
		var v progressView
		if err := json.Unmarshal([]byte(body), &v); err == nil && cond(v) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("задача %s не достигла условия за 5с; последний прогресс: %s", id, last)
}

// addJob добавляет задание media в конфиг окружения.
func addJob(t *testing.T, env *testEnv) {
	t.Helper()
	env.cfg.mu.Lock()
	defer env.cfg.mu.Unlock()
	env.cfg.jobs = append(env.cfg.jobs, domain.Job{
		Name:  "media",
		Mode:  domain.ModeMirror,
		Paths: []string{"/data"},
	})
}

// seedFiles наполняет ФС окружения файлами для бекапа.
func seedFiles(t *testing.T, env *testEnv) {
	t.Helper()
	for p, c := range map[string]string{
		"/data/a.txt":      "содержимое A\n",
		"/data/sub/b.conf": "b = 1\n",
	} {
		if err := env.fs.MkdirAll(dirOf(p), 0o755); err != nil {
			t.Fatal(err)
		}
		wc, err := env.fs.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wc.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
		if err := wc.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// dirOf — каталог пути.
func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}

// formatTape форматирует ленту окружения через API.
func formatTape(t *testing.T, env *testEnv) {
	t.Helper()
	if code, body := do(t, env, http.MethodPost, "/api/tape/format?name=test-tape", ""); code != http.StatusOK {
		t.Fatalf("format: %d %q", code, body)
	}
}

func TestBackupRestore_FullFlow(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		deps.Codec = tapeformat.NewCodec() // настоящий кодек: полный round-trip
	})
	seedFiles(t, env)
	addJob(t, env)
	formatTape(t, env)

	// Бекап.
	code, body := do(t, env, http.MethodPost, "/api/backup/start?job=media&full=true", "")
	if code != http.StatusAccepted || !strings.Contains(body, "task-a") {
		t.Fatalf("backup start: %d %q", code, body)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "success" })

	sessions, err := env.cat.ListSessions(context.Background(), "")
	if err != nil || len(sessions) != 1 || sessions[0].Type != domain.SessionFull {
		t.Fatalf("сессии каталога: %v %v", sessions, err)
	}

	// Полное восстановление в отдельный каталог.
	code, body = do(t, env, http.MethodPost, "/api/restore/start?dest=/restore", "")
	if code != http.StatusAccepted || !strings.Contains(body, "task-b") {
		t.Fatalf("restore start: %d %q", code, body)
	}
	waitFor(t, env, "task-b", func(v progressView) bool { return v.State == "success" })

	for _, p := range []string{"/restore/data/a.txt", "/restore/data/sub/b.conf"} {
		if _, err := env.fs.Stat(p); err != nil {
			t.Errorf("файл %s не восстановлен: %v", p, err)
		}
	}

	// Прогресс неизвестной задачи — 404.
	if code, _ := do(t, env, http.MethodGet, "/api/tasks/no-such/progress", ""); code != http.StatusNotFound {
		t.Errorf("прогресс неизвестной задачи: %d, want 404", code)
	}
}

func TestRestoreStart_SmartByPaths(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		deps.Codec = tapeformat.NewCodec()
	})
	seedFiles(t, env)
	addJob(t, env)
	formatTape(t, env)

	if code, _ := do(t, env, http.MethodPost, "/api/backup/start?job=media", ""); code != http.StatusAccepted {
		t.Fatalf("backup: %d", code)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "success" })

	// Удаляем восстановленный источник и возвращаем один файл smart'ом.
	code, body := do(t, env, http.MethodPost, "/api/restore/start?paths=/data/a.txt&original=true", "")
	if code != http.StatusAccepted || !strings.Contains(body, "task-b") {
		t.Fatalf("restore smart: %d %q", code, body)
	}
	waitFor(t, env, "task-b", func(v progressView) bool { return v.State == "success" })
	if _, err := env.fs.Stat("/data/a.txt"); err != nil {
		t.Errorf("smart не вернул файл по исходному пути: %v", err)
	}
}

func TestBackupStart_UnknownJob_TaskFails(t *testing.T) {
	env := newEnv(t, nil)
	formatTape(t, env)

	if code, _ := do(t, env, http.MethodPost, "/api/backup/start?job=нет-такого", ""); code != http.StatusAccepted {
		t.Fatalf("start: %d", code)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "error" })
	_, body := do(t, env, http.MethodGet, "/api/tasks/task-a/progress", "")
	if !strings.Contains(body, "не найдено") {
		t.Errorf("текст ошибки задачи: %q", body)
	}
}

func TestBackupStart_RequiresJob(t *testing.T) {
	env := newEnv(t, nil)
	if code, _ := do(t, env, http.MethodPost, "/api/backup/start", ""); code != http.StatusBadRequest {
		t.Errorf("start без job: %d, want 400", code)
	}
}

// blockingCodec зависает на WriteSession до закрытия release —
// держит задачу в состоянии running.
type blockingCodec struct {
	port.TapeCodec
	release chan struct{}
}

// WriteSession ждёт release, затем делегирует внутренней реализации.
func (c *blockingCodec) WriteSession(ctx context.Context, tape port.Tape,
	header port.SessionHeader, files []domain.FileMeta, fs port.FileReader,
	prog port.ProgressReporter) error {
	select {
	case <-c.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return c.TapeCodec.WriteSession(ctx, tape, header, files, fs, prog)
}

func TestSingleActiveTask_409AndSettingsBlocked(t *testing.T) {
	release := make(chan struct{})
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		deps.Codec = &blockingCodec{TapeCodec: e.codec, release: release}
	})
	seedFiles(t, env)
	addJob(t, env)
	formatTape(t, env)

	if code, _ := do(t, env, http.MethodPost, "/api/backup/start?job=media", ""); code != http.StatusAccepted {
		t.Fatalf("первая задача: %d", code)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "running" })

	// Вторая ленточная задача — конфликт.
	code, body := do(t, env, http.MethodPost, "/api/backup/start?job=media", "")
	if code != http.StatusConflict || !strings.Contains(body, "task_running") {
		t.Fatalf("вторая задача: %d %q, want 409 task_running", code, body)
	}
	// Смена устройства во время задачи — конфликт.
	code, _ = do(t, env, http.MethodPost, "/api/settings", `{"device":"/dev/nst9"}`)
	if code != http.StatusConflict {
		t.Fatalf("settings при задаче: %d, want 409", code)
	}
	// Список активных задач непуст.
	code, body = do(t, env, http.MethodGet, "/api/tasks/active", "")
	if code != http.StatusOK || !strings.Contains(body, "task-a") {
		t.Fatalf("active: %d %q", code, body)
	}

	close(release)
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "success" })
	// После завершения настройки меняются свободно.
	if code, _ := do(t, env, http.MethodPost, "/api/settings", `{"device":"/dev/nst9"}`); code != http.StatusOK {
		t.Errorf("settings после задачи: %d, want 200", code)
	}
}

func TestTaskRegistry_ConcurrentUpdates(t *testing.T) {
	env := newEnv(t, nil) // нужен только реестр — через сервер
	reg := env.server.Registry()

	const workers = 8
	for i := 0; i < workers; i++ {
		id := string(rune('a' + i))
		reg.Start("task-"+id, "backup", func(task *web.Task) {
			prog := web.NewTaskProgress(task, env.clock)
			for j := 0; j < 200; j++ {
				prog.Update(port.ProgressUpdate{
					Phase:          port.PhaseWrite,
					CurrentFile:    "/data/f",
					ProcessedBytes: int64(j) * 1024,
					TotalBytes:     200 * 1024,
				})
			}
			prog.Done()
		})
	}
	// Параллельные читатели прогресса.
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				for _, task := range reg.Active() {
					_ = task.Snapshot()
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := reg.WaitAll(ctx); err != nil {
		t.Fatalf("WaitAll: %v", err)
	}
	close(stop)
	<-readerDone

	// StartIfIdle проходит после завершения всех задач.
	if err := reg.StartIfIdle("task-z", "backup", func(*web.Task) {}); err != nil {
		t.Fatalf("StartIfIdle после завершения: %v", err)
	}
	if err := reg.WaitAll(ctx); err != nil {
		t.Fatalf("WaitAll хвост: %v", err)
	}
}

func TestTasksActive_Empty(t *testing.T) {
	env := newEnv(t, nil)
	code, body := do(t, env, http.MethodGet, "/api/tasks/active", "")
	if code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Fatalf("active пустой: %d %q", code, body)
	}
}

func TestTaskLogs_Throttled(t *testing.T) {
	env := newEnv(t, nil)
	reg := env.server.Registry()

	if err := reg.StartIfIdle("task-l", "backup", func(tk *web.Task) {
		prog := web.NewTaskProgress(tk, env.clock)
		for j := 0; j < 1000; j++ {
			prog.Update(port.ProgressUpdate{
				Phase:          port.PhaseWrite,
				CurrentFile:    "/data/f",
				ProcessedBytes: int64(j + 1),
				TotalBytes:     1000,
			})
		}
		prog.Done()
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var success bool
	var logs int
	var percent float64
	for time.Now().Before(deadline) {
		task, ok := reg.Get("task-l")
		if !ok {
			t.Fatal("задача не зарегистрирована")
		}
		snap := task.Snapshot()
		logs, percent = len(snap.Logs), snap.Percent
		if snap.State == "success" {
			success = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !success {
		t.Fatal("задача не завершилась успешно")
	}
	if logs > 10 {
		t.Errorf("лог задачи не троттлится: %d строк", logs)
	}
	if percent != 100 {
		t.Errorf("percent=%v, want 100", percent)
	}
}
