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

// REST API демона на chi: маршруты, аутентификация, статика.
// См. docs/SPECIFICATION.md §6 (таблица эндпоинтов), §9.2 (сетевой доступ).

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// ServerConfig — конфигурация, которую читает демон. Реализация —
// adapter/tomlconfig; отдельный интерфейс (а не расширение port),
// чтобы не ломать существующие реализации port.ConfigSource.
type ServerConfig interface {
	port.ConfigSource
	Bind() string
	WebUsername() string
	WebPasswordHash() string
	APIKey() string
	SessionTTL() time.Duration
	RawTOML() (string, error)
}

// Deps — зависимости демона; собираются в iface/cli/wire.go.
type Deps struct {
	Log     *slog.Logger
	Version string
	Config  ServerConfig
	Editor  port.ConfigEditor // add/remove заданий (обычно тот же Config)
	Catalog port.Catalog
	FS      port.Filesystem
	Codec   port.TapeCodec
	Hasher  port.Hasher
	Rand    port.Rand
	Clock   port.Clock

	// OpenTape открывает ленту по пути устройства; probe с дружелюбной
	// ошибкой (usermod -aG tape) входит в реализацию из wire.
	OpenTape func(device string) (port.Tape, error)

	// NewTaskID генерирует идентификаторы задач вида "task-XXXXXXXX".
	NewTaskID func() string

	// BindOverride — адрес из флагов CLI ("host:port"); "" — из конфига.
	BindOverride string
}

// Server — HTTP-демон lentovodec.
type Server struct {
	deps   Deps
	auth   *authManager
	tasks  *TaskRegistry
	router chi.Router
	bind   string
	gate   tapeGate // сериализация доступа к устройству ленты (st: один FD)

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	device string // текущий путь устройства (меняется через POST /settings)
}

// NewServer собирает демона. Отказывает с понятной ошибкой, если bind
// не loopback, а аутентификация не настроена (SPEC §9.2).
func NewServer(deps Deps) (*Server, error) {
	if deps.OpenTape == nil {
		return nil, errors.New("web: Deps.OpenTape не задан")
	}
	if deps.NewTaskID == nil {
		deps.NewTaskID = defaultTaskID
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	bind := deps.Config.Bind()
	if deps.BindOverride != "" {
		bind = deps.BindOverride
	}
	if !isLoopbackBind(bind) &&
		(deps.Config.WebUsername() == "" || deps.Config.WebPasswordHash() == "") {
		return nil, fmt.Errorf(
			"web: bind %s доступен из сети — задайте web_username и web_password_hash "+
				"в конфиге (хеш: lentovodec passwd) или слушайте 127.0.0.1", bind)
	}
	auth := newAuthManager(deps.Log, deps.Clock,
		deps.Config.WebUsername(), deps.Config.WebPasswordHash(),
		deps.Config.APIKey(), deps.Config.SessionTTL())

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		deps:   deps,
		auth:   auth,
		tasks:  NewTaskRegistry(),
		bind:   bind,
		ctx:    ctx,
		cancel: cancel,
		device: deps.Config.Device(),
	}
	s.router = s.buildRouter()
	return s, nil
}

// BindAddr — адрес, на котором демон слушает.
func (s *Server) BindAddr() string { return s.bind }

// Handler возвращает http.Handler демона.
func (s *Server) Handler() http.Handler { return s.router }

// Registry возвращает реестр фоновых задач (диагностика, тесты).
func (s *Server) Registry() *TaskRegistry { return s.tasks }

// buildRouter собирает дерево маршрутов по SPEC §6.
func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(s.requestLogger)

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", s.handleStatus)     // без аутентификации
		r.Post("/auth/login", s.handleLogin) // без аутентификации
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/config", s.handleConfig)
			r.Get("/settings", s.handleSettingsGet)
			r.Post("/settings", s.handleSettingsPost)
			r.Get("/tape/info", s.handleTapeInfo)
			r.Post("/tape/eject", s.handleTapeEject)
			r.Post("/tape/format", s.handleTapeFormat)
			r.Get("/jobs", s.handleJobsList)
			r.Post("/jobs", s.handleJobAdd)
			r.Delete("/jobs/{name}", s.handleJobRemove)
			r.Post("/backup/start", s.handleBackupStart)
			r.Post("/restore/start", s.handleRestoreStart)
			r.Get("/tasks/active", s.handleTasksActive)
			r.Get("/tasks/{id}/progress", s.handleTaskProgress)
			r.Post("/tasks/{id}/continue", s.handleTaskContinue)
			r.Get("/catalog/tapes", s.handleCatalogTapes)
			r.Get("/catalog/sessions", s.handleCatalogSessions)
			r.Get("/catalog/sessions/{id}/files", s.handleSessionFiles)
			r.Get("/catalog/search", s.handleCatalogSearch)
			r.Delete("/catalog/sessions/{id}", s.handleSessionDelete)
			r.Post("/catalog/prune", s.handleCatalogPrune)
		})
	})
	r.Get("/*", s.handleStatic)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not found", "not_found")
	})
	return r
}

// Run слушает bind до отмены ctx, затем глушит HTTP и ждёт задачи
// до 30 секунд (SPEC §9: graceful shutdown).
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.bind)
	if err != nil {
		return fmt.Errorf("web: слушание %s: %w", s.bind, err)
	}
	httpSrv := &http.Server{Handler: s.router} //nolint:gosec // таймауты не критичны для LAN-демона

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.cancel()
		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		s.deps.Log.Warn("web: http shutdown", slog.String("error", err.Error()))
	}
	s.cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer waitCancel()
	if err := s.tasks.WaitAll(waitCtx); err != nil {
		s.deps.Log.Warn("web: фоновые задачи не завершились за 30с")
	}
	return nil
}

// currentDevice — текущий путь устройства.
func (s *Server) currentDevice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.device
}

// setDevice меняет путь устройства.
func (s *Server) setDevice(device string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.device = device
}

// openTape открывает ленту на текущем устройстве. Вызывается только
// владельцем устройства из tapeGate: внутри withTape, задачей (после
// acquireTask) или changer'ом задачи — параллельный open получил бы
// EBUSY от st-драйвера.
func (s *Server) openTape() (port.Tape, error) {
	return s.deps.OpenTape(s.currentDevice())
}

// withTape — короткая операция с лентой: под защитой tapeGate открыть
// устройство, выполнить fn, закрыть. Занятость фоновой задачей —
// tapeBusyError (409), ошибки открытия/операции — как есть.
func (s *Server) withTape(fn func(tape port.Tape) error) error {
	return s.gate.withOp(func() error {
		tape, err := s.openTape()
		if err != nil {
			return err
		}
		defer func() {
			if cerr := tape.Close(); cerr != nil {
				s.deps.Log.Warn("web: закрытие ленты", "error", cerr.Error())
			}
		}()
		return fn(tape)
	})
}

// --- вспомогательные HTTP-функции ---

// writeJSON отправляет v как JSON со статусом status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Соединение уже могло оборваться после заголовка — продолжать
	// некуда, ошибка записи не информативна.
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr отправляет ошибку в формате SPEC §6: {"error", "code"}.
func writeErr(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// writeTapeErr — ошибка операции с лентой в ответе API: занятость
// фоновой задачей — 409 task_running; errno ENOMEDIUM (st-драйвер без
// кассеты, open или ioctl — «no medium found») — 409 no_medium с
// понятным текстом. Строковая проверка errno — конвенция проекта
// (см. mapTapeFull в usecase/backup): iface не импортирует syscall.
func writeTapeErr(w http.ResponseWriter, err error, code string) {
	var busy tapeBusyError
	if errors.As(err, &busy) {
		writeErr(w, http.StatusConflict, busy.Error(), "task_running")
		return
	}
	if strings.Contains(err.Error(), "no medium found") {
		writeErr(w, http.StatusConflict, (&domain.NoMediumError{}).Error(), "no_medium")
		return
	}
	writeErr(w, statusFor(err), err.Error(), code)
}

// statusFor подбирает HTTP-код по доменной ошибке.
func statusFor(err error) int {
	switch {
	case errors.Is(err, &domain.SessionNotFoundError{}),
		errors.Is(err, &domain.TapeNotFoundError{}):
		return http.StatusNotFound
	case errors.Is(err, &domain.AlreadyFormattedError{}),
		errors.Is(err, &domain.BlankTapeError{}),
		errors.Is(err, &domain.ForeignFormatError{}),
		errors.Is(err, &domain.NewerFormatError{}),
		errors.Is(err, &domain.LabelMismatchError{}):
		return http.StatusConflict
	case errors.Is(err, &domain.TapeFullError{}):
		return http.StatusInsufficientStorage
	case errors.Is(err, &domain.NoHealthyCopyError{}):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// isLoopbackBind сообщает, что bind-адрес не торчит в сеть.
func isLoopbackBind(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		host = bind
	}
	if host == "" || host == "*" {
		return false // все интерфейсы
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requestLogger — минимальный лог запросов (без тела).
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.deps.Log.Debug("http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", ww.Status()))
	})
}

// handleStatic отдаёт встроенный Vue-бандл; отсутствующие пути →
// index.html (SPA-роутинг клиента).
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		http.Error(w, "assets unavailable", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(sub, name); err != nil {
		name = "index.html"
	}
	http.ServeFileFS(w, r, sub, name)
}
