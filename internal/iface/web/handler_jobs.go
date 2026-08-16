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

// Эндпоинты заданий: /api/jobs. См. docs/SPECIFICATION.md §6.3.

package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"lentovodec/internal/domain"
)

// jobJSON — задание в JSON (поля — как в TOML, SPEC §2.1).
type jobJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        string   `json:"mode"`
	Paths       []string `json:"paths"`
	Exclude     []string `json:"exclude"`
}

// handleJobsList — GET /api/jobs: список заданий из TOML.
func (s *Server) handleJobsList(w http.ResponseWriter, _ *http.Request) {
	jobs, err := s.deps.Config.Jobs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "internal")
		return
	}
	out := make([]jobJSON, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobJSON{
			Name:        j.Name,
			Description: j.Description,
			Mode:        string(j.Mode),
			Paths:       j.Paths,
			Exclude:     j.Exclude,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleJobAdd — POST /api/jobs: добавить задание в TOML.
func (s *Server) handleJobAdd(w http.ResponseWriter, r *http.Request) {
	var req jobJSON
	if err := decodeJSON(r, &req, maxLoginBody); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "bad_request")
		return
	}
	job := domain.Job{
		Name:        req.Name,
		Description: req.Description,
		Mode:        domain.JobMode(req.Mode),
		Paths:       req.Paths,
		Exclude:     req.Exclude,
	}
	if err := s.deps.Editor.AddJob(job); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "bad_job")
		return
	}
	s.deps.Log.Info("job added",
		"job", job.Name, "user", requestUser(r), "event", "jobs")
	writeJSON(w, http.StatusCreated, req)
}

// handleJobRemove — DELETE /api/jobs/{name}.
func (s *Server) handleJobRemove(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	jobs, err := s.deps.Config.Jobs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "internal")
		return
	}
	found := false
	for _, j := range jobs {
		if j.Name == name {
			found = true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "задание не найдено", "not_found")
		return
	}
	if err := s.deps.Editor.RemoveJob(name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "internal")
		return
	}
	s.deps.Log.Info("job removed",
		"job", name, "user", requestUser(r), "event", "jobs")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
