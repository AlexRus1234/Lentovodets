// Схема каталога (docs/SPECIFICATION.md §3.1). Миграционного движка
// нет: единственная «миграция» — идемпотентный CREATE TABLE IF NOT EXISTS.

package sqlite

import "fmt"

// applySchema создаёт таблицы и индексы каталога, если их ещё нет.
// Вызывается из конструктора при каждом открытии БД.
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
