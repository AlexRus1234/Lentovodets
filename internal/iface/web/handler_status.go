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

// Эндпоинты состояния и настроек: /status, /config, /settings.
// См. docs/SPECIFICATION.md §6.1.

package web

import (
	"net/http"
)

// statusResponse — ответ GET /api/status.
type statusResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Device  string `json:"device"`
	Tape    bool   `json:"tape"` // устройство доступно (probe open)
}

// settingsResponse — ответ GET /api/settings.
type settingsResponse struct {
	Device string `json:"device"`
}

// settingsRequest — тело POST /api/settings.
type settingsRequest struct {
	Device string `json:"device"`
}

// handleStatus — healthcheck без аутентификации: версия и доступность
// устройства (probe = открытие и закрытие).
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	device := s.currentDevice()
	tape := true
	if t, err := s.openTape(); err != nil {
		tape = false
	} else if err := t.Close(); err != nil {
		s.deps.Log.Warn("web: закрытие probe-ленты", "error", err.Error())
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Status: "ok", Version: s.deps.Version, Device: device, Tape: tape,
	})
}

// handleConfig — текущий конфиг как TOML-текст.
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	raw, err := s.deps.Config.RawTOML()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "internal")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// Обрыв соединения после заголовка не информативен.
	_, _ = w.Write([]byte(raw))
}

// handleSettingsGet — текущие настройки устройства.
func (s *Server) handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, settingsResponse{Device: s.currentDevice()})
}

// handleSettingsPost — смена пути устройства на лету; отклоняется,
// пока бегут фоновые задачи (лента одна).
func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := decodeJSON(r, &req, maxLoginBody); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "bad_request")
		return
	}
	if req.Device == "" {
		writeErr(w, http.StatusBadRequest, "пустой путь устройства", "bad_request")
		return
	}
	if s.tasks.HasActive() {
		writeErr(w, http.StatusConflict, "нельзя менять устройство во время задачи", "task_running")
		return
	}
	s.setDevice(req.Device)
	s.deps.Log.Info("device changed",
		"device", req.Device, "user", requestUser(r), "event", "settings")
	writeJSON(w, http.StatusOK, settingsResponse{Device: req.Device})
}
