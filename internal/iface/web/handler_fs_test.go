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

package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lentovodec/internal/iface/web"
	"lentovodec/internal/testutil"
)

func TestFSList_RequiresAuthentication(t *testing.T) {
	env := newEnv(t, func(_ *web.Deps, env *testEnv) {
		env.cfg.username = "admin"
		env.cfg.passHash = "configured"
	})
	resp, err := http.Get(env.srv.URL + "/api/fs/list?path=/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestFSList_ReturnsSortedEntriesAndParent(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, _ *testEnv) {
		deps.FS = testMapFS()
	})
	resp, err := http.Get(env.srv.URL + "/api/fs/list?path=/data/sub")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Path    string `json:"path"`
		Parent  string `json:"parent"`
		Entries []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
			Size  int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/data/sub" || got.Parent != "/data" {
		t.Errorf("path/parent = %q/%q", got.Path, got.Parent)
	}
	if len(got.Entries) != 2 || got.Entries[0].Name != "deep" || !got.Entries[0].IsDir || got.Entries[1].Name != "file.txt" {
		t.Errorf("entries = %+v", got.Entries)
	}
	if got.Entries[1].Size != 3 {
		t.Errorf("file size = %d, want 3", got.Entries[1].Size)
	}
}

func TestFSList_ErrorMapping(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, _ *testEnv) {
		deps.FS = testMapFS()
	})
	cases := []struct {
		path   string
		code   string
		status int
	}{
		{path: "/missing", code: "not_found", status: http.StatusNotFound},
		{path: "/data/file.txt", code: "bad_request", status: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(env.srv.URL + "/api/fs/list?path=" + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status || body.Code != tc.code {
				t.Errorf("response = %d/%q, want %d/%q", resp.StatusCode, body.Code, tc.status, tc.code)
			}
		})
	}
}

func TestFSList_RootParentIsEmpty(t *testing.T) {
	env := newEnv(t, func(deps *web.Deps, _ *testEnv) { deps.FS = testMapFS() })
	req := httptest.NewRequest(http.MethodGet, "/api/fs/list?path=/", nil)
	resp := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		Parent string `json:"parent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Parent != "" {
		t.Errorf("root parent = %q, want empty", body.Parent)
	}
}

func testMapFS() *testutil.MapFS {
	return testutil.NewMapFS(map[string]string{
		"/data/file.txt":     "abc",
		"/data/sub/file.txt": "sub",
		"/data/sub/deep/x":   "x",
	})
}
