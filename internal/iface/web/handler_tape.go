// Эндпоинты ленты: info / eject / format. См. docs/SPECIFICATION.md §6.2.

package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"lentovodec/internal/usecase/catalog"
	"lentovodec/internal/usecase/format"
)

// tapeInfoResponse — ответ GET /api/tape/info.
type tapeInfoResponse struct {
	Label    tapeLabelJSON `json:"label"`
	Filemark int           `json:"filemark"`
}

// tapeLabelJSON — ярлык кассеты в JSON.
type tapeLabelJSON struct {
	Magic         string `json:"magic"`
	FormatVersion int    `json:"format_version"`
	Name          string `json:"name"`
	UUID          string `json:"uuid"`
	FormattedAt   string `json:"formatted_at"`
}

// handleTapeInfo — прочитать ярлык установленной ленты.
func (s *Server) handleTapeInfo(w http.ResponseWriter, r *http.Request) {
	tape, err := s.openTape()
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "tape_open")
		return
	}
	defer func() {
		if err := tape.Close(); err != nil {
			s.deps.Log.Warn("web: закрытие ленты после info", "error", err.Error())
		}
	}()
	uc := catalog.New(s.deps.Catalog, tape, s.deps.Codec, nil, s.deps.Log)
	info, err := uc.TapeInfo(r.Context())
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "tape_info")
		return
	}
	writeJSON(w, http.StatusOK, tapeInfoResponse{
		Label: tapeLabelJSON{
			Magic:         info.Label.Magic,
			FormatVersion: info.Label.FormatVersion,
			Name:          info.Label.Name,
			UUID:          info.Label.UUID,
			FormattedAt:   info.Label.FormattedAt,
		},
		Filemark: info.Filemark,
	})
}

// handleTapeEject — извлечь ленту (MTOFFL).
func (s *Server) handleTapeEject(w http.ResponseWriter, r *http.Request) {
	tape, err := s.openTape()
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "tape_open")
		return
	}
	defer func() {
		if err := tape.Close(); err != nil {
			s.deps.Log.Warn("web: закрытие ленты после eject", "error", err.Error())
		}
	}()
	uc := catalog.New(s.deps.Catalog, tape, s.deps.Codec, nil, s.deps.Log)
	if err := uc.Eject(r.Context()); err != nil {
		writeErr(w, statusFor(err), err.Error(), "tape_eject")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTapeFormat — POST /api/tape/format?name=&force=.
func (s *Server) handleTapeFormat(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "параметр name обязателен", "bad_request")
		return
	}
	force := isTruthy(r.URL.Query().Get("force"))

	tape, err := s.openTape()
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "tape_open")
		return
	}
	defer func() {
		if err := tape.Close(); err != nil {
			s.deps.Log.Warn("web: закрытие ленты после format", "error", err.Error())
		}
	}()
	uc := format.New(tape, s.deps.Codec, s.deps.Catalog, s.deps.Rand, s.deps.Clock, s.deps.Log)
	label, err := uc.Format(r.Context(), name, force)
	if err != nil {
		writeErr(w, statusFor(err), err.Error(), "tape_format")
		return
	}
	s.deps.Log.Info("tape formatted",
		"name", name, "force", force, "user", requestUser(r), "event", "format")
	writeJSON(w, http.StatusOK, tapeLabelJSON{
		Magic:         label.Magic,
		FormatVersion: label.FormatVersion,
		Name:          label.Name,
		UUID:          label.UUID,
		FormattedAt:   label.FormattedAt,
	})
}

// isTruthy разбирает булевы query-параметры.
func isTruthy(v string) bool {
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// urlParamInt64 — число из пути маршрута; false при ошибке разбора.
func urlParamInt64(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
