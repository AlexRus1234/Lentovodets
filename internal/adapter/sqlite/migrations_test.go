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
	"strings"
	"testing"

	// Драйвер для прямых запросов к БД в тестах миграций.
	_ "modernc.org/sqlite"

	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/domain"
)

// oldSchemaDDL — схема каталога до колонки part (user_version = 0).
const oldSchemaDDL = `
CREATE TABLE tapes (
	uuid         TEXT PRIMARY KEY,
	name         TEXT UNIQUE NOT NULL,
	formatted_at INTEGER NOT NULL
);
CREATE TABLE sessions (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	tape_uuid   TEXT NOT NULL,
	session_num INTEGER NOT NULL,
	type        TEXT NOT NULL CHECK (type IN ('FULL', 'INC')),
	timestamp   INTEGER NOT NULL,
	job_run_id  TEXT NOT NULL,
	FOREIGN KEY (tape_uuid) REFERENCES tapes(uuid),
	UNIQUE (tape_uuid, session_num)
);`

const oldSchemaV1DDL = oldSchemaDDL + `
CREATE TABLE files (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	path       TEXT NOT NULL,
	size       INTEGER NOT NULL,
	mod_time   INTEGER NOT NULL,
	is_dir     BOOLEAN NOT NULL,
	hash       TEXT NOT NULL,
	state      TEXT NOT NULL,
	FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);`

// userVersion читает PRAGMA user_version отдельным соединением.
func userVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	return v
}

// TestMigrateV0ToV2_ExistingDB: БД старой схемы открывается конструктором,
// колонки part и special-file добавляются, user_version = 2.
func TestMigrateV0ToV1_ExistingDB(t *testing.T) {
	path := t.TempDir() + "/catalog.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(oldSchemaDDL); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO tapes (uuid, name, formatted_at) VALUES ('tape-1', 'media-001', 1)`); err != nil {
		t.Fatalf("seed tape: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO sessions (tape_uuid, session_num, type, timestamp, job_run_id)
		VALUES ('tape-1', 1, 'FULL', 100, 'run-old'), ('tape-1', 2, 'INC', 200, 'run-old-2')`); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	c, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New на старой БД: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	sessions, err := c.ListSessions(context.Background(), "tape-1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Part != 1 || sessions[1].Part != 1 {
		t.Fatalf("старые строки: %+v, want part=1 у обеих", sessions)
	}
	if v := userVersion(t, path); v != 2 {
		t.Fatalf("user_version = %d, want 2", v)
	}

	// Повторное открытие: миграция не применяется второй раз.
	c2, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New повторно: %v", err)
	}
	if err := c2.Close(); err != nil {
		t.Fatalf("Close повторно: %v", err)
	}
	if v := userVersion(t, path); v != 2 {
		t.Fatalf("user_version после повторного открытия = %d, want 2", v)
	}
}

// TestMigrateV1_ColumnAlreadyExists: user_version = 0 при наличии
// колонки part (БД, созданная актуальной схемой, но с обнулённой
// версией) — ALTER пропускается, версия доводится до 1.
func TestMigrateV1_ColumnAlreadyExists(t *testing.T) {
	path := t.TempDir() + "/catalog.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(oldSchemaDDL); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	if _, err := raw.Exec(`ALTER TABLE sessions ADD COLUMN part INTEGER NOT NULL DEFAULT 1`); err != nil {
		t.Fatalf("add part: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	c, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if v := userVersion(t, path); v != 2 {
		t.Fatalf("user_version = %d, want 2", v)
	}
}

// TestMigrateV1_AlterFails: sessions — не таблица (вьюха); ALTER
// падает посторонней ошибкой, New отказывается от открытия.
func TestMigrateV1_AlterFails(t *testing.T) {
	path := t.TempDir() + "/catalog.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE tapes (uuid TEXT PRIMARY KEY);
		CREATE VIEW sessions AS SELECT * FROM tapes`); err != nil {
		t.Fatalf("seed вьюхи: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	if _, err := sqlite.New(path); err == nil {
		t.Fatal("New на БД с вьюхой sessions = nil, want ошибка миграции")
	} else if !strings.Contains(err.Error(), "sqlite:") {
		t.Fatalf("err = %v, want префикс sqlite:", err)
	}
}

// TestFreshDB_SchemaV2: новая БД создаётся сразу актуальной схемой —
// user_version = 2, колонки part/type/linkname работают без миграций.
func TestFreshDB_SchemaV1(t *testing.T) {
	path := t.TempDir() + "/catalog.db"
	c, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if v := userVersion(t, path); v != 2 {
		t.Fatalf("user_version свежей БД = %d, want 2", v)
	}

	ctx := context.Background()
	if err := c.RegisterTape(ctx, "tape-1", "media-001", 1); err != nil {
		t.Fatalf("RegisterTape: %v", err)
	}
	id, err := c.CreateSession(ctx, domain.Session{
		TapeUUID: "tape-1", Num: 1, Type: domain.SessionFull,
		Timestamp: 100, JobRunID: "run-fresh", Part: 2,
	})
	if err != nil {
		t.Fatalf("CreateSession с part=2: %v", err)
	}
	chain, err := c.GetSessionChain(ctx, "run-fresh")
	if err != nil {
		t.Fatalf("GetSessionChain: %v", err)
	}
	if len(chain) != 1 || chain[0].ID != id || chain[0].Part != 2 {
		t.Fatalf("chain = %+v, want одна сессия part=2", chain)
	}
}

func TestMigrateV1ToV2_DefaultsAndRoundTripSpecialFields(t *testing.T) {
	path := t.TempDir() + "/catalog.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(oldSchemaV1DDL); err != nil {
		t.Fatalf("old v1 schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO tapes VALUES ('tape-1', 'media', 1);
		INSERT INTO sessions (tape_uuid, session_num, type, timestamp, job_run_id) VALUES ('tape-1', 1, 'FULL', 1, 'run');
		INSERT INTO files (session_id, path, size, mod_time, is_dir, hash, state) VALUES (1, '/old', 3, 4, 0, 'hash', 'A');`); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer c.Close()
	files, err := c.GetFilesBySession(context.Background(), 1)
	if err != nil || len(files) != 1 || files[0].Type != domain.TypeReg || files[0].Linkname != "" {
		t.Fatalf("migrated old row = %+v, err=%v", files, err)
	}
	if err := c.SaveFiles(context.Background(), 1, []domain.FileMeta{{Path: "/sym", Type: domain.TypeSym, Linkname: "missing", Size: 7, State: domain.StateAdded}}); err != nil {
		t.Fatal(err)
	}
	files, err = c.GetFilesBySession(context.Background(), 1)
	if err != nil || len(files) != 2 || files[1].Type != domain.TypeSym || files[1].Linkname != "missing" {
		t.Fatalf("special fields round-trip = %+v, err=%v", files, err)
	}
}
