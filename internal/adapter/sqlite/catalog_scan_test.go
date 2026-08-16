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

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/domain"

	// Драйвер для «сырого» второго соединения в тестах ниже.
	_ "modernc.org/sqlite"
)

// TestScanErrors_BadColumnTypes вставляет мимо адаптера (вторым
// соединением) строки с TEXT в INTEGER-колонках: Scan в Go обязан
// упасть, а не вернуть мусор.
func TestScanErrors_BadColumnTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "types.db")
	c, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	ctx := context.Background()
	if err := c.RegisterTape(ctx, "tape-1", "media-001", 1); err != nil {
		t.Fatalf("RegisterTape: %v", err)
	}
	id := mustSession(t, c, domain.Session{
		TapeUUID: "tape-1", Num: 1, Type: domain.SessionFull, Timestamp: 100, JobRunID: "r",
	})

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("raw close: %v", err)
		}
	})
	bad := []string{
		`INSERT INTO files (session_id, path, size, mod_time, is_dir, hash, state)
			VALUES (` + strconv.FormatInt(id, 10) + `, '/bad', 1, 'не-число', 0, 'h', 'A')`,
		`INSERT INTO tapes (uuid, name, formatted_at) VALUES ('bad-tape', 'bad', 'не-число')`,
		`INSERT INTO sessions (tape_uuid, session_num, type, timestamp, job_run_id)
			VALUES ('tape-1', 9, 'INC', 'не-число', 'r')`,
	}
	for _, q := range bad {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("raw вставка %q: %v", q, err)
		}
	}

	cases := []struct {
		name string
		call func() error
	}{
		{"GetFilesBySession", func() error {
			_, err := c.GetFilesBySession(ctx, id)
			return err
		}},
		{"GetLatestFileStates", func() error {
			_, err := c.GetLatestFileStates(ctx, []string{"/bad"})
			return err
		}},
		{"GetAllFileCopies", func() error {
			_, err := c.GetAllFileCopies(ctx, "/bad")
			return err
		}},
		{"SearchFiles", func() error {
			_, err := c.SearchFiles(ctx, "bad")
			return err
		}},
		{"ListTapes", func() error {
			_, err := c.ListTapes(ctx)
			return err
		}},
		{"ListSessions", func() error {
			_, err := c.ListSessions(ctx, "")
			return err
		}},
	}
	for _, tc := range cases {
		if err := tc.call(); err == nil {
			t.Errorf("%s на строке с TEXT в числовой колонке: err = nil, want scan error", tc.name)
		}
	}
}
