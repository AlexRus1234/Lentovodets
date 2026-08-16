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

// Асинхронные задачи: запуск бекапа/восстановления, прогресс.
// См. docs/SPECIFICATION.md §6.4.

package web

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"lentovodec/internal/iface/destfs"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/backup"
	"lentovodec/internal/usecase/restore"
)

// taskIDResponse — ответ на запуск задачи.
type taskIDResponse struct {
	TaskID string `json:"task_id"`
}

// handleBackupStart — POST /api/backup/start?job=&full=.
//
// Фоновая задача открывает ленту на время бекапа и закрывает после:
// устройство не удерживается между операциями.
func (s *Server) handleBackupStart(w http.ResponseWriter, r *http.Request) {
	jobName := r.URL.Query().Get("job")
	if jobName == "" {
		writeErr(w, http.StatusBadRequest, "параметр job обязателен", "bad_request")
		return
	}
	full := isTruthy(r.URL.Query().Get("full"))

	id, err := s.startTapeTask("backup", func(task *Task) {
		tape, err := s.openTape()
		if err != nil {
			task.finishError(err, s.deps.Clock.Now())
			return
		}
		defer closeTape(s, tape)
		uc := backup.New(s.deps.Config, tape, s.deps.Codec, s.deps.Catalog,
			s.deps.FS, s.deps.Hasher, s.deps.Rand, s.deps.Clock,
			NewTaskProgress(task, s.deps.Clock), s.deps.Log)
		if _, err := uc.Backup(s.ctx, jobName, backup.Options{Full: full}); err != nil {
			task.finishError(err, s.deps.Clock.Now()) // идемпотентно после prog.Fail
		}
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error(), "task_running")
		return
	}
	s.deps.Log.Info("backup task started",
		"job", jobName, "full", full, "user", requestUser(r), "event", "task")
	writeJSON(w, http.StatusAccepted, taskIDResponse{TaskID: id})
}

// handleRestoreStart — POST /api/restore/start?paths=&dest=&original=.
//
// paths (через запятую) → smart-восстановление этих путей; без paths —
// полное восстановление ленты. dest — каталог-назначение; original
// восстанавливает по исходным путям из индекса (dest игнорируется).
func (s *Server) handleRestoreStart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dest := q.Get("dest")
	if isTruthy(q.Get("original")) {
		dest = ""
	}
	var paths []string
	for _, p := range strings.Split(q.Get("paths"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}

	id, err := s.startTapeTask("restore", func(task *Task) {
		tape, err := s.openTape()
		if err != nil {
			task.finishError(err, s.deps.Clock.Now())
			return
		}
		defer closeTape(s, tape)
		fs := s.deps.FS
		if dest != "" {
			fs = destfs.Wrap(s.deps.FS, dest)
		}
		uc := restore.New(tape, s.deps.Codec, s.deps.Catalog, fs,
			NewTaskProgress(task, s.deps.Clock), s.deps.Log)
		if len(paths) > 0 {
			_, err = uc.Smart(s.ctx, paths)
		} else {
			_, err = uc.Full(s.ctx)
		}
		if err != nil {
			task.finishError(err, s.deps.Clock.Now()) // идемпотентно после prog.Fail
		}
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error(), "task_running")
		return
	}
	s.deps.Log.Info("restore task started",
		"paths", len(paths), "dest", dest, "user", requestUser(r), "event", "task")
	writeJSON(w, http.StatusAccepted, taskIDResponse{TaskID: id})
}

// handleTasksActive — GET /api/tasks/active.
func (s *Server) handleTasksActive(w http.ResponseWriter, _ *http.Request) {
	active := s.tasks.Active()
	out := make([]taskProgressJSON, 0, len(active))
	for _, t := range active {
		out = append(out, t.Snapshot())
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTaskProgress — GET /api/tasks/{id}/progress.
func (s *Server) handleTaskProgress(w http.ResponseWriter, r *http.Request) {
	task, ok := s.tasks.Get(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "задача не найдена", "not_found")
		return
	}
	writeJSON(w, http.StatusOK, task.Snapshot())
}

// startTapeTask регистрирует фоновую задачу атомарно; отказ, если уже
// есть активная — стример один, параллельные ленточные задачи
// бессмысленны.
func (s *Server) startTapeTask(kind string, fn func(*Task)) (string, error) {
	id := s.deps.NewTaskID()
	if err := s.tasks.StartIfIdle(id, kind, fn); err != nil {
		return "", err
	}
	return id, nil
}

// closeTape закрывает ленту фоновой задачи, логируя сбой.
func closeTape(s *Server, tape port.Tape) {
	if err := tape.Close(); err != nil {
		s.deps.Log.Warn("web: закрытие ленты задачи", "error", err.Error())
	}
}
