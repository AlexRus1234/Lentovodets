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

// Эндпоинты каталога: кассеты, сессии, файлы, поиск, удаление,
// очистка. См. docs/SPECIFICATION.md §6.5.

package web

import (
	"net/http"
	"strconv"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/catalog"
)

// TapeJSON — кассета каталога.
type TapeJSON struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	FormattedAt int64  `json:"formatted_at"`
}

// SessionJSON — сессия бекапа.
type SessionJSON struct {
	ID        int64  `json:"id"`
	TapeUUID  string `json:"tape_uuid"`
	Num       int32  `json:"num"`
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	JobRunID  string `json:"job_run_id"`
	Part      int32  `json:"part"`
}

// FileJSON — файл сессии.
type FileJSON struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
	Hash    string `json:"hash"`
	State   string `json:"state"`
}

// FileCopyJSON — результат поиска: файл + сессия-копия.
type FileCopyJSON struct {
	FileJSON
	SessionID  int64  `json:"session_id"`
	SessionNum int32  `json:"session_num"`
	TapeUUID   string `json:"tape_uuid"`
	TapeName   string `json:"tape_name"`
	Timestamp  int64  `json:"timestamp"`
}

type FileCopiesJSON struct {
	Path   string         `json:"path"`
	Copies []FileCopyJSON `json:"copies"`
}

// catUC — каталожный use case без ленты (для запросов к БД).
func (s *Server) catUC() *catalog.UseCase {
	return catalog.New(s.deps.Catalog, nil, nil, nil, s.deps.Log, nil)
}

// handleCatalogTapes — GET /api/catalog/tapes.
func (s *Server) handleCatalogTapes(w http.ResponseWriter, r *http.Request) {
	tapes, err := s.catUC().ListTapes(r.Context())
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "internal")
		return
	}
	out := make([]TapeJSON, 0, len(tapes))
	for _, t := range tapes {
		out = append(out, TapeJSON{UUID: t.UUID, Name: t.Name, FormattedAt: t.FormattedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCatalogSessions — GET /api/catalog/sessions?tape=.
func (s *Server) handleCatalogSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.catUC().ListSessions(r.Context(), r.URL.Query().Get("tape"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "internal")
		return
	}
	out := make([]SessionJSON, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, SessionJSON{
			ID:        sess.ID,
			TapeUUID:  sess.TapeUUID,
			Num:       sess.Num,
			Type:      string(sess.Type),
			Timestamp: sess.Timestamp,
			JobRunID:  sess.JobRunID,
			Part:      sess.Part,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSessionFiles — GET /api/catalog/sessions/{id}/files.
func (s *Server) handleSessionFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := urlParamInt64(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "некорректный id сессии", "bad_request")
		return
	}
	files, err := s.catUC().GetFiles(r.Context(), id)
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "session_not_found")
		return
	}
	writeJSON(w, http.StatusOK, filesToJSON(files))
}

// handleCatalogSearch — GET /api/catalog/search?q=.
func (s *Server) handleCatalogSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "параметр q обязателен", "bad_request")
		return
	}
	copies, err := s.catUC().Search(r.Context(), q)
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "internal")
		return
	}
	out := make([]FileCopyJSON, 0, len(copies))
	for _, cp := range copies {
		out = append(out, FileCopyJSON{
			FileJSON:   fileToJSON(cp.Meta),
			SessionID:  cp.SessionID,
			SessionNum: cp.SessionNum,
			TapeUUID:   cp.TapeUUID,
			Timestamp:  cp.Timestamp,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCatalogFileCopies — GET /api/catalog/file-copies?path=.
func (s *Server) handleCatalogFileCopies(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "параметр path обязателен", "bad_request")
		return
	}
	copies, err := s.catUC().Copies(r.Context(), path)
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "internal")
		return
	}
	tapes, err := s.deps.Catalog.ListTapes(r.Context())
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "internal")
		return
	}
	names := make(map[string]string, len(tapes))
	for _, tape := range tapes {
		names[tape.UUID] = tape.Name
	}
	out := make([]FileCopyJSON, 0, len(copies))
	for _, cp := range copies {
		out = append(out, FileCopyJSON{
			FileJSON:   fileToJSON(cp.Meta),
			SessionID:  cp.SessionID,
			SessionNum: cp.SessionNum,
			TapeUUID:   cp.TapeUUID,
			TapeName:   names[cp.TapeUUID],
			Timestamp:  cp.Timestamp,
		})
	}
	writeJSON(w, http.StatusOK, FileCopiesJSON{Path: path, Copies: out})
}

// handleSessionDelete — DELETE /api/catalog/sessions/{id}.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := urlParamInt64(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "некорректный id сессии", "bad_request")
		return
	}
	if err := s.catUC().DeleteSession(r.Context(), id); err != nil {
		writeErr(w, statusFor(err), err.Error(), "session_not_found")
		return
	}
	s.deps.Log.Info("session deleted",
		"session_id", id, "user", requestUser(r), "event", "catalog")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCatalogPrune — POST /api/catalog/prune?days=.
func (s *Server) handleCatalogPrune(w http.ResponseWriter, r *http.Request) {
	days, err := parseQueryInt64(r, "days")
	if err != nil || days <= 0 {
		writeErr(w, http.StatusBadRequest, "параметр days — положительное число", "bad_request")
		return
	}
	before := s.deps.Clock.Now().Unix() - days*24*60*60
	deleted, err := s.catUC().Prune(r.Context(), before)
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "internal")
		return
	}
	s.deps.Log.Info("catalog pruned",
		"days", days, "deleted", deleted, "user", requestUser(r), "event", "catalog")
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

// parseQueryInt64 — int64 из query-параметра.
func parseQueryInt64(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
}

// fileToJSON — FileMeta в JSON-вид.
func fileToJSON(fm domain.FileMeta) FileJSON {
	return FileJSON{
		Path:    fm.Path,
		Size:    fm.Size,
		ModTime: fm.ModTime,
		IsDir:   fm.IsDir,
		Hash:    fm.Hash,
		State:   string(fm.State),
	}
}

// filesToJSON — список FileMeta в JSON-вид.
func filesToJSON(files []domain.FileMeta) []FileJSON {
	out := make([]FileJSON, 0, len(files))
	for _, fm := range files {
		out = append(out, fileToJSON(fm))
	}
	return out
}
