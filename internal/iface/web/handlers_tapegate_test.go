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

// HTTP-тесты сериализации доступа к стримеру (tapeGate): параллельные
// запросы не открывают устройство дважды (st: EBUSY), во время фоновой
// задачи ленточные операции — 409, ENOMEDIUM — понятный текст.

package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
)

// countTape — обёртка ленты окружения: считает одновременно открытые
// ленты (открытие — OpenTape, закрытие — Close). Перекрытие >1 — это и
// есть EBUSY-гонка на реальном стримере.
type countTape struct {
	port.Tape
	cur        *atomic.Int32
	violations *atomic.Int32
}

// Rewind замедляется, чтобы окно перекрытия открытий было заметным.
func (t *countTape) Rewind(ctx context.Context) error {
	time.Sleep(5 * time.Millisecond)
	return t.Tape.Rewind(ctx)
}

// Close уменьшает счётчик открытых лент.
func (t *countTape) Close() error {
	t.cur.Add(-1)
	return t.Tape.Close()
}

// TestTapeOps_ConcurrentNoOverlappingOpen — параллельные status/info/
// format выполняются без одновременных открытий устройства: до
// сериализации probe статуса (каждые 15 с из UI) сталкивался с
// tape info на open /dev/nst0 (EBUSY).
func TestTapeOps_ConcurrentNoOverlappingOpen(t *testing.T) {
	var cur, violations atomic.Int32
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		deps.OpenTape = func(string) (port.Tape, error) {
			if cur.Add(1) > 1 {
				violations.Add(1)
			}
			return &countTape{Tape: e.tape, cur: &cur, violations: &violations}, nil
		}
	})

	// Кассета существует — info отвечает 200 в каждом запросе.
	if code, body := do(t, env, http.MethodPost, "/api/tape/format?name=T-1", ""); code != http.StatusOK {
		t.Fatalf("исходный format: %d %s", code, body)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			code, body := do(t, env, http.MethodGet, "/api/status", "")
			if code != http.StatusOK || strings.Contains(body, "\"tape\":false") {
				t.Errorf("status: %d %s", code, body)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			code, body := do(t, env, http.MethodGet, "/api/tape/info", "")
			if code != http.StatusOK || strings.Contains(body, "tape_open") {
				t.Errorf("info: %d %s", code, body)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			code, body := do(t, env, http.MethodPost, "/api/tape/format?name=T-1&force=true", "")
			if code != http.StatusOK {
				t.Errorf("format: %d %s", code, body)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := violations.Load(); got != 0 {
		t.Fatalf("одновременных открытий устройства: %d, want 0 (гонка EBUSY)", got)
	}
	if got := cur.Load(); got != 0 {
		t.Fatalf("незакрытых лент после запросов: %d, want 0", got)
	}
}

// TestTape_NoMedium409 — ENOMEDIUM (привод без кассеты; адаптер
// linuxtape типизирует его в *domain.NoMediumError) показывается
// понятным текстом с code=no_medium вместо сырого errno.
func TestTape_NoMedium409(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, _ *testEnv) {
		deps.OpenTape = func(string) (port.Tape, error) {
			return nil, errors.Join(&domain.NoMediumError{}, fmt.Errorf(
				"linuxtape: открытие /dev/nst0: open /dev/nst0: no medium found"))
		}
	})

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/tape/info"},
		{http.MethodPost, "/api/tape/eject"},
		{http.MethodPost, "/api/tape/format?name=X"},
	} {
		code, body := do(t, env, tc.method, tc.path, "")
		if code != http.StatusConflict {
			t.Errorf("%s: %d, want 409 (%s)", tc.path, code, body)
		}
		if !strings.Contains(body, "no_medium") ||
			!strings.Contains(body, "нет кассеты в приводе") {
			t.Errorf("%s: тело %s, want no_medium + понятный текст", tc.path, body)
		}
	}

	// Probe статуса честно показывает tape=false.
	code, body := do(t, env, http.MethodGet, "/api/status", "")
	if code != http.StatusOK || !strings.Contains(body, "\"tape\":false") {
		t.Errorf("status без кассеты: %d %s, want tape:false", code, body)
	}
}

// TestTapeOps_BusyDuringTask409 — задача владеет устройством: tape/*
// откатываются 409 task_running, probe статуса не ждёт и видит
// устройство; после задачи операции возвращаются.
func TestTapeOps_BusyDuringTask409(t *testing.T) {
	release := make(chan struct{})
	opened := make(chan struct{})
	var once sync.Once
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		inner := deps.OpenTape
		deps.OpenTape = func(d string) (port.Tape, error) {
			once.Do(func() { close(opened) })
			<-release // задача держит устройство, пока тест проверяет 409
			return inner(d)
		}
	})

	code, body := do(t, env, http.MethodPost, "/api/restore/start", "")
	if code != http.StatusAccepted {
		t.Fatalf("restore start: %d %s", code, body)
	}
	var started struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("разбор task_id: %v", err)
	}
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("задача не открыла устройство за 5с")
	}

	// Задача владеет устройством: ленточные операции — 409 task_running.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/tape/info"},
		{http.MethodPost, "/api/tape/eject"},
		{http.MethodPost, "/api/tape/format?name=X"},
	} {
		code, body := do(t, env, tc.method, tc.path, "")
		if code != http.StatusConflict || !strings.Contains(body, "task_running") {
			t.Errorf("%s при задаче: %d %s, want 409 task_running", tc.path, code, body)
		}
	}

	// Probe не блокируется задачей: устройство доступно (открыто задачей).
	code, body = do(t, env, http.MethodGet, "/api/status", "")
	if code != http.StatusOK || strings.Contains(body, "\"tape\":false") {
		t.Errorf("status при задаче: %d %s, want tape:true без ожидания", code, body)
	}

	close(release)
	waitFor(t, env, started.TaskID, func(v progressView) bool {
		return v.State == "error" || v.State == "success"
	})

	// Устройство освобождено: 409 может быть только по делу (пустая
	// лента), но не task_running.
	code, body = do(t, env, http.MethodGet, "/api/tape/info", "")
	if strings.Contains(body, "task_running") {
		t.Errorf("info после задачи: %d %s, device должен быть свободен", code, body)
	}
}
