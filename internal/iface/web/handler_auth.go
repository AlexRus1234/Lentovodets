// Эндпоинты аутентификации и middleware. См. docs/SPECIFICATION.md §6.0.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxLoginBody — лимит тела логина (защита от исчерпания памяти).
const maxLoginBody = 4 << 10

// loginRequest — тело POST /api/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse — ответ успешного логина.
type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// ctxKey — тип ключей контекста запроса.
type ctxKey int

// userKey — имя пользователя (сессии или "api-key") в контексте.
const userKey ctxKey = iota

// handleLogin — POST /api/auth/login с rate-limit 5/30c на IP.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req, maxLoginBody); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "bad_request")
		return
	}
	token, expiresAt, err := s.auth.Login(req.Username, req.Password, clientIP(r))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt.UTC().Format(time.RFC3339)})
	case errors.Is(err, errAuthDisabled):
		writeErr(w, http.StatusBadRequest, err.Error(), "auth_disabled")
	case errors.Is(err, errRateLimited), errors.Is(err, errTooManySessions):
		writeErr(w, http.StatusTooManyRequests, err.Error(), "rate_limited")
	case errors.Is(err, errInvalidCredentials):
		writeErr(w, http.StatusUnauthorized, err.Error(), "invalid_credentials")
	default:
		writeErr(w, http.StatusInternalServerError, err.Error(), "internal")
	}
}

// handleLogout — POST /api/auth/logout удаляет текущую сессию.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := bearerToken(r); tok != "" {
		s.auth.Logout(tok)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// authMiddleware пропускает запрос с валидной сессией или API-ключом;
// иначе 401 (SPEC §6.0). При выключенной аутентификации пропускает
// всех (loopback bind проверен в NewServer).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.enabled() {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, "anonymous")))
			return
		}
		if tok := bearerToken(r); tok != "" && s.auth.ValidSession(tok) {
			user := s.auth.SessionUser(tok)
			if user == "" {
				user = "session"
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
			return
		}
		if key := r.Header.Get("X-API-Key"); s.auth.ValidAPIKey(key) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, "api-key")))
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "auth_required")
	})
}

// requestUser — имя пользователя из контекста (для аудита).
func requestUser(r *http.Request) string {
	if v, ok := r.Context().Value(userKey).(string); ok {
		return v
	}
	return "unknown"
}

// bearerToken достаёт токен из "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// clientIP — адрес клиента для rate-limit и аудита (без Proxy-заголовков:
// демон — инструмент одной машины).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// decodeJSON читает тело запроса ограниченного размера в v.
func decodeJSON(r *http.Request, v any, limit int64) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return errors.New("не удалось прочитать тело запроса")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("некорректный JSON в теле запроса")
	}
	return nil
}
