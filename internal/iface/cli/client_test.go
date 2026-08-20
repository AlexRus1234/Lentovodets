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

// Тесты HTTPClient против httptest-демона: авторизация API-ключом,
// fallback на логин, ошибки демона.

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lentovodec/internal/iface/cli"
)

// newFakeAPIServer — демон-заглушка: требует X-API-Key «key» или
// Bearer-токен, иначе 401; /api/auth/login пускает admin/secret.
func newFakeAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	reply := func(w http.ResponseWriter, status int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Username == "admin" && body.Password == "secret" {
			reply(w, http.StatusOK, `{"token":"tok","expires_at":"2030-01-01T00:00:00Z"}`)
			return
		}
		reply(w, http.StatusUnauthorized, `{"error":"неверный логин или пароль","code":"invalid_credentials"}`)
	})
	withKey := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != "key" && r.Header.Get("Authorization") != "Bearer tok" {
				reply(w, http.StatusUnauthorized, `{"error":"unauthorized","code":"auth_required"}`)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/tape/info", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `{"label":{"magic":"M","format_version":2,"name":"T1","uuid":"u1","formatted_at":"x"},"filemark":-1}`)
	}))
	mux.HandleFunc("POST /api/tape/eject", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `{"status":"ok"}`)
	}))
	mux.HandleFunc("GET /api/catalog/tapes", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `[{"uuid":"u1","name":"T1","formatted_at":1}]`)
	}))
	mux.HandleFunc("GET /api/catalog/sessions", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `[{"id":7,"tape_uuid":"u1","num":1,"type":"FULL","timestamp":1,"job_run_id":"r"}]`)
	}))
	mux.HandleFunc("GET /api/catalog/sessions/7/files", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `[{"path":"/a","size":1,"mod_time":0,"is_dir":false,"hash":"h","state":"A"}]`)
	}))
	mux.HandleFunc("GET /api/catalog/search", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `[{"path":"/a","size":1,"mod_time":0,"is_dir":false,"hash":"h","state":"A","session_id":7,"session_num":1,"tape_uuid":"u1","timestamp":1}]`)
	}))
	mux.HandleFunc("GET /api/catalog/file-copies", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `{"path":"/a","copies":[]}`)
	}))
	mux.HandleFunc("DELETE /api/catalog/sessions/7", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `{"status":"ok"}`)
	}))
	mux.HandleFunc("POST /api/catalog/prune", withKey(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, `{"deleted":3}`)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPClient_APIKeyAuth(t *testing.T) {
	srv := newFakeAPIServer(t)
	c := cli.Dial(srv.URL, "key", "", nil)
	ctx := context.Background()

	info, err := c.TapeInfo(ctx)
	if err != nil || info.Label.Name != "T1" || info.Label.UUID != "u1" {
		t.Fatalf("TapeInfo: %v %+v", err, info)
	}
	if err := c.Eject(ctx); err != nil {
		t.Fatalf("Eject: %v", err)
	}
	tapes, err := c.ListTapes(ctx)
	if err != nil || len(tapes) != 1 || tapes[0].Name != "T1" {
		t.Fatalf("ListTapes: %v %v", err, tapes)
	}
	sessions, err := c.ListSessions(ctx, "")
	if err != nil || len(sessions) != 1 || sessions[0].ID != 7 {
		t.Fatalf("ListSessions: %v %v", err, sessions)
	}
	filtered, err := c.ListSessions(ctx, "u1")
	if err != nil || len(filtered) != 1 {
		t.Fatalf("ListSessions filter: %v %v", err, filtered)
	}
	files, err := c.SessionFiles(ctx, 7)
	if err != nil || len(files) != 1 || files[0].Path != "/a" {
		t.Fatalf("SessionFiles: %v %v", err, files)
	}
	copies, err := c.Search(ctx, "a")
	if err != nil || len(copies) != 1 || copies[0].SessionNum != 1 {
		t.Fatalf("Search: %v %v", err, copies)
	}
	copies, err = c.Copies(ctx, "/a")
	if err != nil || len(copies) != 0 {
		t.Fatalf("Copies: %v %v", err, copies)
	}
	if err := c.DeleteSession(ctx, 7); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	deleted, err := c.Prune(ctx, 30)
	if err != nil || deleted != 3 {
		t.Fatalf("Prune: %v %d", err, deleted)
	}
}

func TestHTTPClient_LoginFallback(t *testing.T) {
	srv := newFakeAPIServer(t)
	askCount := 0
	c := cli.Dial(srv.URL, "", "admin", func() (string, error) {
		askCount++
		return "secret", nil
	})

	// Первый запрос без ключа → 401 → логин → повтор с токеном.
	if _, err := c.TapeInfo(context.Background()); err != nil {
		t.Fatalf("TapeInfo с логином: %v", err)
	}
	if askCount != 1 {
		t.Fatalf("AskPassword вызван %d раз, want 1", askCount)
	}
}

func TestHTTPClient_LoginUnavailable(t *testing.T) {
	srv := newFakeAPIServer(t)
	c := cli.Dial(srv.URL, "", "", nil) // ни ключа, ни пароля

	_, err := c.TapeInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "авторизацию") {
		t.Fatalf("хотели понятную ошибку авторизации, получили: %v", err)
	}
}

func TestHTTPClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"внутренняя ошибка","code":"internal"}`))
	}))
	t.Cleanup(srv.Close)
	c := cli.Dial(srv.URL, "key", "", nil)

	if err := c.Eject(context.Background()); err == nil || !strings.Contains(err.Error(), "внутренняя ошибка") {
		t.Fatalf("текст ошибки демона потерян: %v", err)
	}
}

func TestHTTPClient_Unreachable(t *testing.T) {
	c := cli.Dial("http://127.0.0.1:1", "key", "", nil)
	if _, err := c.TapeInfo(context.Background()); err == nil {
		t.Fatal("недоступный демон = nil")
	}
}
