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

// Сквозные сценарии spanning в демоне (сессия 7 плана): бекап в две
// кассеты с паузой awaiting_tape и продолжением по REST, restore по
// цепочке кассет, аутентификация/аудит continue, гонки continue.

package web_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	tapeformat "lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// tapeQueue — фабрика лент для OpenTape: выдаёт кассеты по порядку,
// исчерпав очередь — свежую чистую FakeTape.
type tapeQueue struct {
	mu    sync.Mutex
	tapes []port.Tape
}

func (q *tapeQueue) set(tapes ...port.Tape) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tapes = tapes
}

func (q *tapeQueue) open(string) (port.Tape, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.tapes) > 0 {
		t := q.tapes[0]
		q.tapes = q.tapes[1:]
		return t, nil
	}
	return testutil.NewFakeTape(), nil
}

// captureLog — slog.Handler, запоминающий записи как строки
// «сообщение|k=v|...» (проверка аудит-лога).
type captureLog struct {
	mu      sync.Mutex
	records []string
}

func (c *captureLog) Enabled(context.Context, slog.Level) bool { return true }

func (c *captureLog) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		sb.WriteString("|")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
		return true
	})
	c.mu.Lock()
	c.records = append(c.records, sb.String())
	c.mu.Unlock()
	return nil
}

func (c *captureLog) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *captureLog) WithGroup(string) slog.Handler      { return c }

// find сообщает, есть ли запись, содержащая все подстроки.
func (c *captureLog) find(subs ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
outer:
	for _, rec := range c.records {
		for _, s := range subs {
			if !strings.Contains(rec, s) {
				continue outer
			}
		}
		return true
	}
	return false
}

// spanEnv — окружение для spanning-сценариев: настоящий кодек
// (round-trip по лентам), кассеты из очереди, capacity 24 байта
// (файлы 23+6 байт → две части). Лента не форматирована: тесты
// делают это сами (в auth-сценарии — с заголовком).
func spanEnv(
	t *testing.T,
	tapes []port.Tape,
	mutate func(deps *web.Deps, env *testEnv),
) (*testEnv, *tapeQueue) {
	t.Helper()
	q := &tapeQueue{}
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		deps.Codec = tapeformat.NewCodec()
		deps.OpenTape = q.open
		// UUID по порядку потребления: кассета t1 → jobRunID → кассета t2
		// (FixedRand без аргументов дал бы обеим кассетам один UUID).
		deps.Rand = testutil.FixedRand(
			"11111111-1111-4111-8111-111111111111",
			"22222222-2222-4222-8222-222222222222",
			"33333333-3333-4333-8333-333333333333")
		e.cfg.capacity = 24
		if mutate != nil {
			mutate(deps, e)
		}
	})
	q.set(tapes...)
	seedFiles(t, env)
	addJob(t, env)
	return env, q
}

// doWith — запрос с дополнительными заголовками (аутентификация).
func doWith(t *testing.T, env *testEnv, method, path, body string, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, env.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// waitForH — waitFor с заголовками аутентификации.
func waitForH(t *testing.T, env *testEnv, id string, hdr map[string]string, cond func(progressView) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		_, last = doWith(t, env, http.MethodGet, "/api/tasks/"+id+"/progress", "", hdr)
		var v progressView
		if err := json.Unmarshal([]byte(last), &v); err == nil && cond(v) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("задача %s не достигла условия за 5с; последний прогресс: %s", id, last)
}

// startSpanBackup запускает бекап media (FULL) и ждёт паузы на смену
// кассеты; hdr — заголовки запросов (аутентификация).
func startSpanBackup(t *testing.T, env *testEnv, hdr map[string]string) {
	t.Helper()
	code, body := doWith(t, env, http.MethodPost, "/api/backup/start?job=media&full=true", "", hdr)
	if code != http.StatusAccepted || !strings.Contains(body, "task-a") {
		t.Fatalf("backup start: %d %q", code, body)
	}
	waitForH(t, env, "task-a", hdr, func(v progressView) bool { return v.State == "awaiting_tape" })
	_, body = doWith(t, env, http.MethodGet, "/api/tasks/task-a/progress", "", hdr)
	for _, sub := range []string{"awaiting_tape", "media-002", "suggested_tape_name"} {
		if !strings.Contains(body, sub) {
			t.Fatalf("прогресс awaiting_tape: %q, нет %q", body, sub)
		}
	}
}

func TestSpanningBackup_ContinueOverAPI(t *testing.T) {
	t1, t2 := testutil.NewFakeTape(), testutil.NewFakeTape()
	env, _ := spanEnv(t, []port.Tape{t1, t1, t2}, nil)
	formatTape(t, env)
	startSpanBackup(t, env, nil)

	// awaiting_tape — активная задача: параллельный запуск и смена
	// устройства заблокированы (стример один).
	if code, body := do(t, env, http.MethodPost, "/api/backup/start?job=media", ""); code != http.StatusConflict ||
		!strings.Contains(body, "task_running") {
		t.Fatalf("второй бекап в awaiting: %d %q, want 409 task_running", code, body)
	}
	if code, _ := do(t, env, http.MethodPost, "/api/settings", `{"device":"/dev/nst9"}`); code != http.StatusConflict {
		t.Fatalf("settings в awaiting: %d, want 409", code)
	}
	if code, body := do(t, env, http.MethodGet, "/api/tasks/active", ""); code != http.StatusOK ||
		!strings.Contains(body, "awaiting_tape") {
		t.Fatalf("active в awaiting: %d %q", code, body)
	}

	// Продолжение без имени — предложенное (media-002: у test-tape нет
	// числового суффикса → <job>-002).
	code, body := do(t, env, http.MethodPost, "/api/tasks/task-a/continue", "")
	if code != http.StatusOK || !strings.Contains(body, "media-002") {
		t.Fatalf("continue: %d %q, want 200 media-002", code, body)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "success" })

	// Каталог: цепочка из двух частей на двух кассетах, один запуск.
	ctx := context.Background()
	sessions, err := env.cat.ListSessions(ctx, "")
	if err != nil || len(sessions) != 2 {
		t.Fatalf("сессии: %v %v", sessions, err)
	}
	chain, err := env.cat.GetSessionChain(ctx, sessions[0].JobRunID)
	if err != nil || len(chain) != 2 {
		t.Fatalf("цепочка: %v %v", chain, err)
	}
	if chain[0].Part != 1 || chain[1].Part != 2 || chain[0].TapeUUID == chain[1].TapeUUID {
		t.Errorf("цепочка частей: %+v", chain)
	}
	tapes, err := env.cat.ListTapes(ctx)
	if err != nil || len(tapes) != 2 {
		t.Fatalf("касеты каталога: %v %v", tapes, err)
	}
	names := tapes[0].Name + "," + tapes[1].Name
	if !strings.Contains(names, "test-tape") || !strings.Contains(names, "media-002") {
		t.Errorf("касеты каталога: %s", names)
	}

	// Ошибки continue: завершённая задача — 409, битое тело — 400,
	// неизвестная задача — 404.
	if code, _ := do(t, env, http.MethodPost, "/api/tasks/task-a/continue", `{"tape_name":"X"}`); code != http.StatusConflict {
		t.Errorf("continue завершённой: %d, want 409", code)
	}
	if code, _ := do(t, env, http.MethodPost, "/api/tasks/task-a/continue", `{битый`); code != http.StatusBadRequest {
		t.Errorf("continue с битым телом: %d, want 400", code)
	}
	if code, _ := do(t, env, http.MethodPost, "/api/tasks/no-such/continue", ""); code != http.StatusNotFound {
		t.Errorf("continue неизвестной: %d, want 404", code)
	}
}

func TestSpanningRestore_ContinueOverAPI(t *testing.T) {
	t1, t2 := testutil.NewFakeTape(), testutil.NewFakeTape()
	env, q := spanEnv(t, []port.Tape{t1, t1, t2}, nil)
	formatTape(t, env)

	// Фаза записи: две части на две кассеты.
	startSpanBackup(t, env, nil)
	if code, _ := do(t, env, http.MethodPost, "/api/tasks/task-a/continue", ""); code != http.StatusOK {
		t.Fatalf("continue бекапа: %d", code)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "success" })

	// Фаза чтения: сначала кассета части 1, по указателю продолжения —
	// пауза на вставку media-002, затем часть 2.
	q.set(t1, t2)
	code, body := do(t, env, http.MethodPost, "/api/restore/start?dest=/out", "")
	if code != http.StatusAccepted || !strings.Contains(body, "task-b") {
		t.Fatalf("restore start: %d %q", code, body)
	}
	waitFor(t, env, "task-b", func(v progressView) bool { return v.State == "awaiting_tape" })
	_, body = do(t, env, http.MethodGet, "/api/tasks/task-b/progress", "")
	if !strings.Contains(body, "прочитана") || !strings.Contains(body, "media-002") {
		t.Fatalf("сообщение restore-паузы: %q", body)
	}
	if code, _ := do(t, env, http.MethodPost, "/api/tasks/task-b/continue", ""); code != http.StatusOK {
		t.Fatalf("continue restore: %d", code)
	}
	waitFor(t, env, "task-b", func(v progressView) bool { return v.State == "success" })

	// Файлы обеих частей восстановлены.
	for _, p := range []string{"/out/data/a.txt", "/out/data/sub/b.conf"} {
		if _, err := env.fs.Stat(p); err != nil {
			t.Errorf("файл %s не восстановлен: %v", p, err)
		}
	}
}

func TestTaskContinue_Auth401AndAudit(t *testing.T) {
	t1, t2 := testutil.NewFakeTape(), testutil.NewFakeTape()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	logs := &captureLog{}
	env, _ := spanEnv(t, []port.Tape{t1, t1, t2}, func(deps *web.Deps, e *testEnv) {
		e.cfg.username = "admin"
		e.cfg.passHash = string(hash)
		e.cfg.apiKey = "script-key"
		deps.Log = slog.New(logs)
	})

	hdr := map[string]string{"X-API-Key": "script-key"}
	if code, body := doWith(t, env, http.MethodPost, "/api/tape/format?name=test-tape", "", hdr); code != http.StatusOK {
		t.Fatalf("format: %d %q", code, body)
	}
	startSpanBackup(t, env, hdr)

	// Без аутентификации продолжение не проходит.
	if code, _ := do(t, env, http.MethodPost, "/api/tasks/task-a/continue", ""); code != http.StatusUnauthorized {
		t.Fatalf("continue без auth: %d, want 401", code)
	}
	// Публичный API-ключ допустим (скриптовая смена кассеты).
	if code, _ := doWith(t, env, http.MethodPost, "/api/tasks/task-a/continue", "", hdr); code != http.StatusOK {
		t.Fatalf("continue по API-ключу: %d, want 200", code)
	}
	waitForH(t, env, "task-a", hdr, func(v progressView) bool { return v.State == "success" })

	if !logs.find("task continue", "event=task_continue", "user=api-key", "tape=media-002") {
		t.Error("аудит-лог не содержит запись task_continue")
	}
}

func TestTaskContinue_ConcurrentContinueAndPolling(t *testing.T) {
	t1, t2 := testutil.NewFakeTape(), testutil.NewFakeTape()
	env, _ := spanEnv(t, []port.Tape{t1, t1, t2}, nil)
	formatTape(t, env)
	startSpanBackup(t, env, nil)

	const workers = 8
	results := make(chan int, workers)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = do(t, env, http.MethodGet, "/api/tasks/task-a/progress", "")
					_, _ = do(t, env, http.MethodGet, "/api/tasks/active", "")
				}
			}
		}()
	}
	var continuers sync.WaitGroup
	for i := 0; i < workers; i++ {
		continuers.Add(1)
		go func() {
			defer continuers.Done()
			code, _ := do(t, env, http.MethodPost, "/api/tasks/task-a/continue", `{"tape_name":"media-002"}`)
			results <- code
		}()
	}
	continuers.Wait()
	close(results)
	close(stop)
	readers.Wait()

	ok, conflict := 0, 0
	for code := range results {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("continue: неожиданный код %d", code)
		}
	}
	if ok != 1 || conflict != workers-1 {
		t.Errorf("continue: ok=%d conflict=%d, want 1 и %d", ok, conflict, workers-1)
	}
	waitFor(t, env, "task-a", func(v progressView) bool { return v.State == "success" })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := env.server.Registry().WaitAll(ctx); err != nil {
		t.Fatalf("WaitAll: %v", err)
	}
}
