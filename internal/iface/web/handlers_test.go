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

// Handler-тесты REST API: статус, конфиг, настройки, лента, задания,
// каталог, статика.

package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/iface/web"
)

// do выполняет запрос к серверу окружения и возвращает статус и тело.
func do(t *testing.T, env *testEnv, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, env.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func TestStatus(t *testing.T) {
	env := newEnv(t, nil)
	code, body := do(t, env, http.MethodGet, "/api/status", "")
	if code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["version"] != "test" || resp["tape"] != true {
		t.Errorf("status = %v", resp)
	}
}

func TestCatalogFileCopies(t *testing.T) {
	env := newEnv(t, func(_ *web.Deps, env *testEnv) {
		if err := env.cat.RegisterTape(context.Background(), "u1", "LTO-001", 1); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		first, err := env.cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 1, Type: domain.SessionFull, Timestamp: 1, JobRunID: "run-1"})
		if err != nil {
			t.Fatal(err)
		}
		second, err := env.cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 2, Type: domain.SessionInc, Timestamp: 2, JobRunID: "run-2"})
		if err != nil {
			t.Fatal(err)
		}
		if err := env.cat.SaveFiles(ctx, first, []domain.FileMeta{{Path: "/a", Size: 3, State: domain.StateAdded, Hash: "old"}}); err != nil {
			t.Fatal(err)
		}
		if err := env.cat.SaveFiles(ctx, second, []domain.FileMeta{{Path: "/a", Size: 4, State: domain.StateModified, Hash: "new"}}); err != nil {
			t.Fatal(err)
		}
	})
	if code, _ := do(t, env, http.MethodGet, "/api/catalog/file-copies", ""); code != http.StatusBadRequest {
		t.Fatalf("empty path: %d", code)
	}
	code, body := do(t, env, http.MethodGet, "/api/catalog/file-copies?path=%2Fa", "")
	var response web.FileCopiesJSON
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || len(response.Copies) != 2 || response.Copies[0].SessionNum != 2 || response.Copies[0].Hash != "new" || response.Copies[0].TapeName != "LTO-001" {
		t.Fatalf("copies: %d %s", code, body)
	}
	code, body = do(t, env, http.MethodGet, "/api/catalog/file-copies?path=%2Fmissing", "")
	if code != http.StatusOK || !strings.Contains(body, `"copies":[]`) {
		t.Fatalf("missing: %d %s", code, body)
	}
}

func TestConfig_ReturnsTOML(t *testing.T) {
	env := newEnv(t, nil)
	code, body := do(t, env, http.MethodGet, "/api/config", "")
	if code != http.StatusOK || !strings.Contains(body, `/dev/nst0`) {
		t.Fatalf("config: %d %q", code, body)
	}
}

func TestSettings_GetPost(t *testing.T) {
	env := newEnv(t, nil)

	code, body := do(t, env, http.MethodGet, "/api/settings", "")
	if code != http.StatusOK || !strings.Contains(body, "/dev/nst0") {
		t.Fatalf("settings: %d %q", code, body)
	}
	code, body = do(t, env, http.MethodPost, "/api/settings", `{"device":"/dev/nst5"}`)
	if code != http.StatusOK || !strings.Contains(body, "/dev/nst5") {
		t.Fatalf("settings post: %d %q", code, body)
	}
	code, body = do(t, env, http.MethodGet, "/api/settings", "")
	if code != http.StatusOK || !strings.Contains(body, "/dev/nst5") {
		t.Fatalf("после смены: %d %q", code, body)
	}

	// Пустой путь устройства отклоняется.
	if code, _ := do(t, env, http.MethodPost, "/api/settings", `{"device":""}`); code != http.StatusBadRequest {
		t.Errorf("пустой device: %d, want 400", code)
	}
	// Битый JSON отклоняется.
	if code, _ := do(t, env, http.MethodPost, "/api/settings", `{`); code != http.StatusBadRequest {
		t.Errorf("битый JSON: %d, want 400", code)
	}
}

func TestTape_FormatInfoEject(t *testing.T) {
	env := newEnv(t, nil)

	code, body := do(t, env, http.MethodPost, "/api/tape/format?name=LTO-001", "")
	if code != http.StatusOK || !strings.Contains(body, "LTO-001") {
		t.Fatalf("format: %d %q", code, body)
	}
	// Кассета появилась в каталоге.
	tapes, err := env.cat.ListTapes(context.Background())
	if err != nil || len(tapes) != 1 || tapes[0].Name != "LTO-001" {
		t.Fatalf("каталог после format: %v %v", tapes, err)
	}

	code, body = do(t, env, http.MethodGet, "/api/tape/info", "")
	if code != http.StatusOK || !strings.Contains(body, "LTO-001") || !strings.Contains(body, `"alerts":[]`) {
		t.Fatalf("info: %d %q", code, body)
	}

	// Повторное форматирование без force — конфликт.
	if code, _ := do(t, env, http.MethodPost, "/api/tape/format?name=LTO-002", ""); code != http.StatusConflict {
		t.Errorf("повторный format: %d, want 409", code)
	}
	if code, _ := do(t, env, http.MethodPost, "/api/tape/format?name=LTO-002&force=true", ""); code != http.StatusOK {
		t.Errorf("format force: %d, want 200", code)
	}

	// Отсутствующее имя — 400.
	if code, _ := do(t, env, http.MethodPost, "/api/tape/format", ""); code != http.StatusBadRequest {
		t.Errorf("format без name: %d, want 400", code)
	}

	if code, _ := do(t, env, http.MethodPost, "/api/tape/eject", ""); code != http.StatusOK {
		t.Errorf("eject: %d", code)
	}
	if !env.tape.Ejected() {
		t.Error("лента не извлечена")
	}
}

func TestTapeInfo_BlankTape_409(t *testing.T) {
	env := newEnv(t, nil) // лента пуста: ярлыка нет
	if code, _ := do(t, env, http.MethodGet, "/api/tape/info", ""); code != http.StatusConflict {
		t.Errorf("info на пустой ленте: %d, want 409", code)
	}
}

func TestJobs_Endpoints(t *testing.T) {
	env := newEnv(t, nil)

	code, body := do(t, env, http.MethodGet, "/api/jobs", "")
	if code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Fatalf("jobs пустой: %d %q", code, body)
	}

	job := `{"name":"media","description":"сериалы","mode":"append","paths":["/tank/media"],"exclude":["*.tmp"]}`
	if code, _ := do(t, env, http.MethodPost, "/api/jobs", job); code != http.StatusCreated {
		t.Fatalf("jobs add: %d", code)
	}
	// Дубликат отклоняется.
	if code, _ := do(t, env, http.MethodPost, "/api/jobs", job); code != http.StatusBadRequest {
		t.Errorf("дубликат: %d, want 400", code)
	}
	// Невалидное задание отклоняется.
	if code, _ := do(t, env, http.MethodPost, "/api/jobs", `{"name":"x","mode":"нет","paths":["/x"]}`); code != http.StatusBadRequest {
		t.Errorf("невалидное: %d, want 400", code)
	}

	code, body = do(t, env, http.MethodGet, "/api/jobs", "")
	if code != http.StatusOK || !strings.Contains(body, "media") {
		t.Fatalf("jobs после add: %d %q", code, body)
	}

	if code, _ := do(t, env, http.MethodDelete, "/api/jobs/нет-такого", ""); code != http.StatusNotFound {
		t.Errorf("remove несуществующего: %d, want 404", code)
	}
	if code, _ := do(t, env, http.MethodDelete, "/api/jobs/media", ""); code != http.StatusOK {
		t.Errorf("remove: %d", code)
	}
	if jobs, _ := env.cfg.Jobs(); len(jobs) != 0 {
		t.Errorf("задание не удалено: %v", jobs)
	}
}

// seedCatalog готовит кассету/сессию/файлы в каталоге окружения.
func seedCatalog(t *testing.T, env *testEnv) int64 {
	t.Helper()
	ctx := context.Background()
	if err := env.cat.RegisterTape(ctx, "uuid-A", "tape-a", 1700000000); err != nil {
		t.Fatal(err)
	}
	id, err := env.cat.CreateSession(ctx, domain.Session{
		TapeUUID: "uuid-A", Num: 1, Type: domain.SessionFull,
		Timestamp: 1700000100, JobRunID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.cat.SaveFiles(ctx, id, []domain.FileMeta{
		{Path: "/data/a.txt", Size: 10, State: domain.StateAdded},
		{Path: "/data/b.txt", Size: 20, State: domain.StateAdded},
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCatalog_Endpoints(t *testing.T) {
	env := newEnv(t, nil)
	sessID := seedCatalog(t, env)

	code, body := do(t, env, http.MethodGet, "/api/catalog/tapes", "")
	if code != http.StatusOK || !strings.Contains(body, "tape-a") {
		t.Fatalf("tapes: %d %q", code, body)
	}

	code, body = do(t, env, http.MethodGet, "/api/catalog/sessions?tape=uuid-A", "")
	if code != http.StatusOK || !strings.Contains(body, `"num":1`) || !strings.Contains(body, `"part":1`) {
		t.Fatalf("sessions: %d %q", code, body)
	}
	code, body = do(t, env, http.MethodGet, "/api/catalog/sessions", "")
	if code != http.StatusOK || !strings.Contains(body, "uuid-A") {
		t.Fatalf("sessions все: %d %q", code, body)
	}

	code, _ = do(t, env, http.MethodGet, "/api/catalog/sessions/999/files", "")
	if code != http.StatusNotFound {
		t.Fatalf("files нет сессии: %d, want 404", code)
	}
	code, body = do(t, env, http.MethodGet,
		"/api/catalog/sessions/"+strconv.FormatInt(sessID, 10)+"/files", "")
	if code != http.StatusOK || !strings.Contains(body, "/data/a.txt") {
		t.Fatalf("files: %d %q", code, body)
	}
	code, _ = do(t, env, http.MethodGet, "/api/catalog/sessions/abc/files", "")
	if code != http.StatusBadRequest {
		t.Fatalf("files битый id: %d, want 400", code)
	}

	code, body = do(t, env, http.MethodGet, "/api/catalog/search?q=a.txt", "")
	if code != http.StatusOK || !strings.Contains(body, "a.txt") {
		t.Fatalf("search: %d %q", code, body)
	}
	if code, _ := do(t, env, http.MethodGet, "/api/catalog/search", ""); code != http.StatusBadRequest {
		t.Errorf("search без q: %d, want 400", code)
	}

	// Prune с нулевым окном ничего не удаляет (сессия «сегодня»).
	code, body = do(t, env, http.MethodPost, "/api/catalog/prune?days=1", "")
	if code != http.StatusOK || !strings.Contains(body, `"deleted":0`) {
		t.Fatalf("prune свежий: %d %q", code, body)
	}
	// Сдвигаем часы демона: сессия стала старше дня.
	env.clock.Add(48 * time.Hour)
	code, body = do(t, env, http.MethodPost, "/api/catalog/prune?days=1", "")
	if code != http.StatusOK || !strings.Contains(body, `"deleted":1`) {
		t.Fatalf("prune: %d %q", code, body)
	}
	if code, _ := do(t, env, http.MethodPost, "/api/catalog/prune?days=0", ""); code != http.StatusBadRequest {
		t.Errorf("prune days=0: %d, want 400", code)
	}
}

func TestSessionDelete(t *testing.T) {
	env := newEnv(t, nil)
	sessID := seedCatalog(t, env)

	if code, _ := do(t, env, http.MethodDelete, "/api/catalog/sessions/999", ""); code != http.StatusNotFound {
		t.Fatalf("удаление несуществующей: %d, want 404", code)
	}
	if code, _ := do(t, env, http.MethodDelete, "/api/catalog/sessions/"+strconv.FormatInt(sessID, 10), ""); code != http.StatusOK {
		t.Fatalf("удаление: %d", code)
	}
	sessions, err := env.cat.ListSessions(context.Background(), "")
	if err != nil || len(sessions) != 0 {
		t.Fatalf("сессия не удалена: %v %v", sessions, err)
	}
}

func TestStatic_ServesIndex(t *testing.T) {
	env := newEnv(t, nil)

	code, body := do(t, env, http.MethodGet, "/", "")
	if code != http.StatusOK || !strings.Contains(body, "lentovodec") {
		t.Fatalf("index: %d %q", code, body)
	}
	// Неизвестный путь отдаёт index.html (SPA-роутинг).
	code, body = do(t, env, http.MethodGet, "/jobs/screen", "")
	if code != http.StatusOK || !strings.Contains(body, "lentovodec") {
		t.Fatalf("SPA fallback: %d %q", code, body)
	}
	// Неизвестный API-путь — JSON 404.
	code, body = do(t, env, http.MethodGet, "/api/unknown", "")
	if code != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Fatalf("api 404: %d %q", code, body)
	}
}
