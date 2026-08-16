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

// HTTP-клиент демона для команд daemon-режима (SPEC §5). Авторизация
// (SPEC §6.0): сначала API-ключ из TOML (X-API-Key), при отказе —
// логин по паролю (интерактивный ввод) и Bearer-токен сессии.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"lentovodec/internal/domain"
	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
)

// HTTPClient — реализация ServerClient поверх REST API демона.
type HTTPClient struct {
	base     string // http://host:port
	apiKey   string
	username string
	ask      func() (string, error) // ввод пароля; nil — неинтерактивно
	token    string
	hc       *http.Client
}

// Dial создаёт клиента демона по адресу url.
func Dial(url, apiKey, username string, askPassword func() (string, error)) ServerClient {
	return &HTTPClient{
		base:     url,
		apiKey:   apiKey,
		username: username,
		ask:      askPassword,
		hc:       &http.Client{},
	}
}

// TapeInfo — GET /api/tape/info.
func (c *HTTPClient) TapeInfo(ctx context.Context) (domain.TapeInfo, error) {
	var resp struct {
		Label    domain.TapeLabel `json:"label"`
		Filemark int              `json:"filemark"`
	}
	if err := c.get(ctx, "/api/tape/info", &resp); err != nil {
		return domain.TapeInfo{}, err
	}
	return domain.TapeInfo{Label: resp.Label, Filemark: resp.Filemark}, nil
}

// Eject — POST /api/tape/eject.
func (c *HTTPClient) Eject(ctx context.Context) error {
	return c.post(ctx, "/api/tape/eject", nil, nil)
}

// ListTapes — GET /api/catalog/tapes.
func (c *HTTPClient) ListTapes(ctx context.Context) ([]port.TapeRecord, error) {
	var resp []web.TapeJSON
	if err := c.get(ctx, "/api/catalog/tapes", &resp); err != nil {
		return nil, err
	}
	out := make([]port.TapeRecord, 0, len(resp))
	for _, t := range resp {
		out = append(out, port.TapeRecord{UUID: t.UUID, Name: t.Name, FormattedAt: t.FormattedAt})
	}
	return out, nil
}

// ListSessions — GET /api/catalog/sessions?tape=.
func (c *HTTPClient) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	path := "/api/catalog/sessions"
	if tapeUUID != "" {
		path += "?tape=" + url.QueryEscape(tapeUUID)
	}
	var resp []web.SessionJSON
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.Session, 0, len(resp))
	for _, s := range resp {
		out = append(out, domain.Session{
			ID:        s.ID,
			TapeUUID:  s.TapeUUID,
			Num:       s.Num,
			Type:      domain.SessionType(s.Type),
			Timestamp: s.Timestamp,
			JobRunID:  s.JobRunID,
		})
	}
	return out, nil
}

// SessionFiles — GET /api/catalog/sessions/{id}/files.
func (c *HTTPClient) SessionFiles(ctx context.Context, sessionID int64) ([]domain.FileMeta, error) {
	var resp []web.FileJSON
	if err := c.get(ctx, fmt.Sprintf("/api/catalog/sessions/%d/files", sessionID), &resp); err != nil {
		return nil, err
	}
	out := make([]domain.FileMeta, 0, len(resp))
	for _, f := range resp {
		out = append(out, fileFromJSON(f))
	}
	return out, nil
}

// Search — GET /api/catalog/search?q=.
func (c *HTTPClient) Search(ctx context.Context, pattern string) ([]port.FileCopy, error) {
	path := "/api/catalog/search?q=" + url.QueryEscape(pattern)
	var resp []web.FileCopyJSON
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	out := make([]port.FileCopy, 0, len(resp))
	for _, cp := range resp {
		out = append(out, port.FileCopy{
			Meta:       fileFromJSON(cp.FileJSON),
			SessionID:  cp.SessionID,
			SessionNum: cp.SessionNum,
			TapeUUID:   cp.TapeUUID,
			Timestamp:  cp.Timestamp,
		})
	}
	return out, nil
}

// DeleteSession — DELETE /api/catalog/sessions/{id}.
func (c *HTTPClient) DeleteSession(ctx context.Context, sessionID int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/catalog/sessions/%d", sessionID), nil, nil)
}

// Prune — POST /api/catalog/prune?days=; «сейчас» считает демон.
func (c *HTTPClient) Prune(ctx context.Context, days int64) (int64, error) {
	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	if err := c.post(ctx, fmt.Sprintf("/api/catalog/prune?days=%d", days), nil, &resp); err != nil {
		return 0, err
	}
	return resp.Deleted, nil
}

// fileFromJSON — файл из JSON-ответа демона.
func fileFromJSON(f web.FileJSON) domain.FileMeta {
	return domain.FileMeta{
		Path:    f.Path,
		Size:    f.Size,
		ModTime: f.ModTime,
		IsDir:   f.IsDir,
		Hash:    f.Hash,
		State:   domain.FileState(f.State),
	}
}

// get — GET с разбором JSON-ответа.
func (c *HTTPClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// post — POST с JSON-телом (nil — пусто) и разбором ответа.
func (c *HTTPClient) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// do выполняет запрос; на 401 — одна попытка войти по паролю и
// повторить (токен/API-ключ могли протухнуть или отсутствовать).
func (c *HTTPClient) do(ctx context.Context, method, path string, body, out any) error {
	status, raw, err := c.roundtrip(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		if lerr := c.tryLogin(ctx); lerr != nil {
			return fmt.Errorf(
				"демон требует авторизацию (%w): задайте api_key в lentovodec.toml или web_username/web_password_hash",
				lerr)
		}
		status, raw, err = c.roundtrip(ctx, method, path, body)
		if err != nil {
			return err
		}
	}
	return parseResponse(status, raw, out)
}

// roundtrip — один HTTP-обмен с текущими кредами (сессионный токен,
// иначе API-ключ); тело ответа читается целиком и закрывается здесь.
func (c *HTTPClient) roundtrip(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("cli: кодирование запроса: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return 0, nil, fmt.Errorf("cli: запрос %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("cli: обмен с демоном: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("cli: чтение ответа: %w", err)
	}
	return res.StatusCode, raw, nil
}

// tryLogin входит по паролю и запоминает токен сессии.
func (c *HTTPClient) tryLogin(ctx context.Context) error {
	if c.ask == nil || c.username == "" {
		return fmt.Errorf("интерактивный вход недоступен")
	}
	password, err := c.ask()
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"username": c.username, "password": password})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("вход отклонён (HTTP %d)", res.StatusCode)
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return err
	}
	c.token = resp.Token
	return nil
}

// parseResponse разбирает статус и тело: 2xx → out (если задан),
// иначе — ошибка с текстом из {"error"} демона.
func parseResponse(status int, raw []byte, out any) error {
	if status >= 200 && status < 300 {
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("cli: разбор ответа: %w", err)
		}
		return nil
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return fmt.Errorf("демон: %s", e.Error)
	}
	return fmt.Errorf("демон: HTTP %d", status)
}
