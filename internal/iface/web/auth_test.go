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

// Тесты аутентификации: bcrypt-логин, rate-limit, сессии с TTL,
// API-ключ, отказ старта при LAN-bind без пароля.

package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
)

// authEnv — окружение с включённой аутентификацией.
func authEnv(t *testing.T, user, pass, apiKey string) *testEnv {
	t.Helper()
	hash := ""
	if pass != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("bcrypt: %v", err)
		}
		hash = string(h)
	}
	return newEnv(t, func(deps *web.Deps, env *testEnv) {
		env.cfg.username = user
		env.cfg.passHash = hash
		env.cfg.apiKey = apiKey
	})
}

// login выполняет POST /api/auth/login.
func login(t *testing.T, env *testEnv, user, pass string) (int, string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	res, err := http.Post(env.srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
		Error     string `json:"error"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("login decode: %v", err)
	}
	return res.StatusCode, resp.Token, resp.Code
}

// getWith выполняет GET с заголовками авторизации.
func getWith(t *testing.T, url string, hdr map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func TestLogin_SuccessCreatesSession(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")

	code, token, _ := login(t, env, "admin", "secret")
	if code != http.StatusOK || token == "" {
		t.Fatalf("code=%d token=%q, want 200 и токен", code, token)
	}
	if code := getWith(t, env.srv.URL+"/api/settings", map[string]string{"Authorization": "Bearer " + token}); code != http.StatusOK {
		t.Errorf("авторизованный запрос: %d, want 200", code)
	}
}

func TestLogin_WrongPassword_401(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")

	code, _, codeStr := login(t, env, "admin", "неверно")
	if code != http.StatusUnauthorized || codeStr != "invalid_credentials" {
		t.Fatalf("code=%d codeStr=%q, want 401 invalid_credentials", code, codeStr)
	}
	if code := getWith(t, env.srv.URL+"/api/settings", nil); code != http.StatusUnauthorized {
		t.Errorf("без заголовка: %d, want 401", code)
	}
}

func TestLogin_RateLimit_429(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")

	for i := 0; i < 5; i++ {
		code, _, _ := login(t, env, "admin", "неверно")
		if code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: code=%d, want 401", i+1, code)
		}
	}
	code, _, codeStr := login(t, env, "admin", "неверно")
	if code != http.StatusTooManyRequests || codeStr != "rate_limited" {
		t.Fatalf("шестая попытка: code=%d codeStr=%q, want 429 rate_limited", code, codeStr)
	}
	// Правильный пароль тоже отклоняется, пока окно не закрылось.
	code, _, _ = login(t, env, "admin", "secret")
	if code != http.StatusTooManyRequests {
		t.Fatalf("логин под лимитом: code=%d, want 429", code)
	}
}

func TestLogin_SuccessResetsRateCounter(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")

	for i := 0; i < 4; i++ {
		login(t, env, "admin", "неверно")
	}
	if code, _, _ := login(t, env, "admin", "secret"); code != http.StatusOK {
		t.Fatalf("успешный вход: %d, want 200", code)
	}
	// Счётчик сброшен: ещё 4 неудачи не дают 429.
	for i := 0; i < 4; i++ {
		if code, _, _ := login(t, env, "admin", "неверно"); code != http.StatusUnauthorized {
			t.Fatalf("попытка %d после сброса: code=%d, want 401", i+1, code)
		}
	}
}

func TestLogin_RateLimit_WindowSlides(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")

	for i := 0; i < 5; i++ {
		login(t, env, "admin", "неверно")
	}
	if code, _, _ := login(t, env, "admin", "secret"); code != http.StatusTooManyRequests {
		t.Fatal("хотели 429 до конца окна")
	}
	env.clock.Add(31 * time.Second) // окно 30 с закрылось
	if code, _, _ := login(t, env, "admin", "secret"); code != http.StatusOK {
		t.Fatalf("после окна: %d, want 200", code)
	}
}

func TestSession_TTLExpiry(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, e *testEnv) {
		hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("bcrypt: %v", err)
		}
		e.cfg.username = "admin"
		e.cfg.passHash = string(hash)
		e.cfg.ttl = time.Hour // TTL до сборки сервера
	})

	_, token, _ := login(t, env, "admin", "secret")
	url := env.srv.URL + "/api/settings"
	if code := getWith(t, url, map[string]string{"Authorization": "Bearer " + token}); code != http.StatusOK {
		t.Fatalf("до истечения: %d", code)
	}
	env.clock.Add(2 * time.Hour)
	if code := getWith(t, url, map[string]string{"Authorization": "Bearer " + token}); code != http.StatusUnauthorized {
		t.Fatalf("после истечения: %d, want 401", code)
	}
}

func TestAuth_APIKey(t *testing.T) {
	env := authEnv(t, "admin", "secret", "script-key")
	url := env.srv.URL + "/api/settings"

	if code := getWith(t, url, map[string]string{"X-API-Key": "script-key"}); code != http.StatusOK {
		t.Errorf("верный ключ: %d, want 200", code)
	}
	if code := getWith(t, url, map[string]string{"X-API-Key": "wrong"}); code != http.StatusUnauthorized {
		t.Errorf("неверный ключ: %d, want 401", code)
	}
	// Пустой ключ заголовком не проходит.
	if code := getWith(t, url, map[string]string{"X-API-Key": ""}); code != http.StatusUnauthorized {
		t.Errorf("пустой ключ: %d, want 401", code)
	}
}

func TestAuth_DisabledOnLoopback(t *testing.T) {
	env := newEnv(t, nil) // без username/hash

	if code := getWith(t, env.srv.URL+"/api/settings", nil); code != http.StatusOK {
		t.Errorf("auth выключен на loopback: %d, want 200", code)
	}
	if code, _, codeStr := login(t, env, "admin", "x"); code != http.StatusBadRequest || codeStr != "auth_disabled" {
		t.Errorf("login при выключенной auth: %d %q, want 400 auth_disabled", code, codeStr)
	}
}

func TestNewServer_LANBindWithoutAuth_Refused(t *testing.T) {
	cases := []struct {
		bind    string
		user    string
		hash    string
		wantErr bool
	}{
		{"0.0.0.0:29201", "", "", true},
		{"192.168.1.10:29201", "", "", true},
		{"192.168.1.10:29201", "admin", "", true},
		{"192.168.1.10:29201", "", "$2a$hash", true},
		{"192.168.1.10:29201", "admin", "$2a$hash", false},
		{"127.0.0.1:29201", "", "", false},
		{"localhost:29201", "", "", false},
	}
	for _, tc := range cases {
		cfg := newFakeConfig()
		cfg.bind = tc.bind
		cfg.username = tc.user
		cfg.passHash = tc.hash
		_, err := web.NewServer(web.Deps{
			Config:   cfg,
			OpenTape: func(string) (port.Tape, error) { return nil, nil },
		})
		if tc.wantErr && err == nil {
			t.Errorf("bind %s user=%q: ожидан отказ старта", tc.bind, tc.user)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("bind %s user=%q: неожиданный отказ: %v", tc.bind, tc.user, err)
		}
	}
}

func TestLogout_DeletesSession(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")
	_, token, _ := login(t, env, "admin", "secret")
	url := env.srv.URL + "/api/settings"
	hdr := map[string]string{"Authorization": "Bearer " + token}

	req, err := http.NewRequest(http.MethodPost, env.srv.URL+"/api/auth/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	if code := getWith(t, url, hdr); code != http.StatusUnauthorized {
		t.Errorf("после logout: %d, want 401", code)
	}
}

func TestAuth_LoginBodyMalformed(t *testing.T) {
	env := authEnv(t, "admin", "secret", "")
	res, err := http.Post(env.srv.URL+"/api/auth/login", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("битый JSON: %d, want 400", res.StatusCode)
	}
}
