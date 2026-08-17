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

// Схема каталога (docs/SPECIFICATION.md §3.1) и её миграции.
// Миграционный движок — PRAGMA user_version: номер схемы в заголовке
// БД, список миграций применяется в конструкторе до CREATE TABLE.
// Свежая БД создаётся сразу актуальной схемой (CREATE TABLE IF NOT
// EXISTS), миграции для неё — no-op.

package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

// applySchema создаёт таблицы и индексы каталога, если их ещё нет.
// Вызывается из конструктора при каждом открытии БД (после миграций).
func (c *Catalog) applySchema() error {
	for _, stmt := range schemaStatements() {
		if _, err := c.db.Exec(stmt); err != nil {
			return fmt.Errorf("sqlite: схема каталога: %w", err)
		}
	}
	return nil
}

// schemaStatements возвращает DDL каталога в порядке зависимостей.
func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS tapes (
			uuid         TEXT PRIMARY KEY,
			name         TEXT UNIQUE NOT NULL,
			formatted_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			tape_uuid   TEXT NOT NULL,
			session_num INTEGER NOT NULL,
			type        TEXT NOT NULL CHECK (type IN ('FULL', 'INC')),
			timestamp   INTEGER NOT NULL,
			job_run_id  TEXT NOT NULL,
			part        INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (tape_uuid) REFERENCES tapes(uuid),
			UNIQUE (tape_uuid, session_num)
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id INTEGER NOT NULL,
			path       TEXT NOT NULL,
			size       INTEGER NOT NULL,
			mod_time   INTEGER NOT NULL,
			is_dir     BOOLEAN NOT NULL,
			hash       TEXT NOT NULL,
			state      TEXT NOT NULL CHECK (state IN ('A', 'M', 'D')),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_files_path    ON files(path)`,
		`CREATE INDEX IF NOT EXISTS idx_files_session ON files(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_tape ON sessions(tape_uuid, session_num)`,
	}
}

// applyMigrations доводит существующую БД до актуальной версии схемы:
// применяет миграции с user_version+1 по последнюю, каждую в своей
// транзакции вместе с записью нового user_version.
func (c *Catalog) applyMigrations() error {
	var current int
	if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("sqlite: чтение user_version: %w", err)
	}
	list := migrationList()
	for version := current; version < len(list); version++ {
		tx, err := c.db.Begin()
		if err != nil {
			return fmt.Errorf("sqlite: миграция v%d: %w", version+1, err)
		}
		if err := list[version](tx); err != nil {
			return rollback(tx, fmt.Errorf("sqlite: миграция v%d: %w", version+1, err))
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version+1)); err != nil {
			return rollback(tx, fmt.Errorf("sqlite: миграция v%d: %w", version+1, err))
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: миграция v%d: %w", version+1, err)
		}
	}
	return nil
}

// migrationList возвращает миграции по порядку; индекс элемента +1 —
// версия схемы, которую миграция устанавливает.
func migrationList() []func(tx *sql.Tx) error {
	return []func(tx *sql.Tx) error{
		migrateV1,
	}
}

// migrateV1: v0 → v1 — колонка part таблицы sessions (номер части
// цепочки spanning-запуска, docs/SPECIFICATION.md §3.1). Существующие
// строки получают 1 (DEFAULT): до spanning каждая сессия — часть 1.
// ALTER — единственная операция; «no such table» (свежая БД, таблицы
// создаст applySchema) и «duplicate column name» (колонка уже есть)
// — не ошибки. Сопоставление по тексту ошибки SQLite — тот же приём,
// что контракт ENOSPC в filetape: адаптер не создаёт domain-ошибок,
// а переводы текстов драйвера стабильны.
func migrateV1(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE sessions
		ADD COLUMN part INTEGER NOT NULL DEFAULT 1`)
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "no such table") || strings.Contains(msg, "duplicate column name") {
		return nil
	}
	return fmt.Errorf("sqlite: ALTER sessions ADD part: %w", err)
}
