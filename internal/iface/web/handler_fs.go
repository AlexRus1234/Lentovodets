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

// Эндпоинт листинга файловой системы. Доступ ограничен общей
// аутентификацией API; allowlist корней намеренно не вводится в v1.

package web

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"syscall"
)

type fsEntryJSON struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
}

type fsListJSON struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent"`
	Entries []fsEntryJSON `json:"entries"`
}

// handleFSList — GET /api/fs/list?path=/abs. Эндпоинт читает ФС сервера
// от лица демона: доступ к дереву равен доступу администратора.
func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	requested := r.URL.Query().Get("path")
	if requested == "" {
		requested = "/"
	}
	entries, err := s.deps.FS.ReadDir(requested)
	if err != nil {
		// Some Filesystem implementations report a file passed to ReadDir
		// with a platform-specific error. Stat gives the API a stable 400.
		if info, statErr := s.deps.FS.Stat(requested); statErr == nil && !info.IsDir() {
			writeErr(w, http.StatusBadRequest, err.Error(), "bad_request")
			return
		}
		status, code := fsErrorResponse(err)
		writeErr(w, status, err.Error(), code)
		return
	}
	clean := path.Clean(requested)
	if clean == "." {
		clean = "/"
	}
	parent := ""
	if clean != "/" {
		parent = path.Dir(clean)
	}
	out := make([]fsEntryJSON, 0, len(entries))
	for _, entry := range entries {
		out = append(out, fsEntryJSON{
			Name: entry.Name, IsDir: entry.IsDir, Size: entry.Size,
			ModTime: entry.ModTime.UnixNano(),
		})
	}
	writeJSON(w, http.StatusOK, fsListJSON{Path: clean, Parent: parent, Entries: out})
}

func fsErrorResponse(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, fs.ErrPermission):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, fs.ErrInvalid):
		return http.StatusBadRequest, "bad_request"
	default:
		return http.StatusInternalServerError, "internal"
	}
}
