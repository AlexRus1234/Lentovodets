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

// Реализация port.Catalog поверх modernc.org/sqlite (чистый Go, без CGO).

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	// Драйвер modernc.org/sqlite: регистрирует имя "sqlite" в database/sql.
	_ "modernc.org/sqlite"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// Каталог держит одно постоянное соединение: PRAGMA foreign_keys
// действует на уровне соединения, а каталог — инструмент одного
// пользователя (docs/SPECIFICATION.md §9.1).
const (
	insertTapeSQL = `INSERT INTO tapes (uuid, name, formatted_at) VALUES (?, ?, ?)
		ON CONFLICT (uuid) DO UPDATE
		SET name = excluded.name, formatted_at = excluded.formatted_at`

	selectTapeSQL = `SELECT uuid, name, formatted_at FROM tapes WHERE uuid = ?`

	insertSessionSQL = `INSERT INTO sessions (tape_uuid, session_num, type, timestamp, job_run_id, part)
		VALUES (?, ?, ?, ?, ?, ?)`

	selectLastSessionNumSQL = `SELECT COALESCE(MAX(session_num), 0) FROM sessions WHERE tape_uuid = ?`

	insertFileSQL = `INSERT INTO files (session_id, path, size, mod_time, is_dir, hash, state, type, linkname)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	selectLatestStatesSQL = `SELECT f.path, f.size, f.mod_time, f.is_dir, f.hash, f.state, f.type, f.linkname
		FROM files f
		JOIN sessions s ON s.id = f.session_id
		WHERE f.path IN (%s)
		ORDER BY f.path ASC, s.timestamp DESC, s.id DESC`

	selectTapesSQL = `SELECT uuid, name, formatted_at FROM tapes ORDER BY name`

	selectSessionsSQL = `SELECT id, tape_uuid, session_num, type, timestamp, job_run_id, part
		FROM sessions WHERE tape_uuid = ? ORDER BY session_num`

	selectAllSessionsSQL = `SELECT id, tape_uuid, session_num, type, timestamp, job_run_id, part
		FROM sessions ORDER BY tape_uuid, session_num`

	selectSessionChainSQL = `SELECT id, tape_uuid, session_num, type, timestamp, job_run_id, part
		FROM sessions WHERE job_run_id = ?
		ORDER BY part ASC, tape_uuid ASC, session_num ASC`

	sessionExistsSQL = `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`

	selectFilesBySessionSQL = `SELECT path, size, mod_time, is_dir, hash, state, type, linkname
		FROM files WHERE session_id = ? ORDER BY path`

	selectFileCopiesSQL = `SELECT f.path, f.size, f.mod_time, f.is_dir, f.hash, f.state, f.type, f.linkname,
			s.id, s.session_num, s.tape_uuid, s.timestamp
		FROM files f
		JOIN sessions s ON s.id = f.session_id
		WHERE f.path = ?
		ORDER BY s.timestamp DESC, s.id DESC`

	searchFilesSQL = `SELECT f.path, f.size, f.mod_time, f.is_dir, f.hash, f.state, f.type, f.linkname,
			s.id, s.session_num, s.tape_uuid, s.timestamp
		FROM files f
		JOIN sessions s ON s.id = f.session_id
		WHERE f.path LIKE ? ESCAPE '\'
		ORDER BY f.path ASC, s.timestamp DESC, s.id DESC`

	deleteSessionSQL = `DELETE FROM sessions WHERE id = ?`

	pruneSessionsSQL = `DELETE FROM sessions WHERE timestamp < ?`
)

// Catalog — реализация port.Catalog. Все методы безопасны для
// конкурентного вызова (database/sql).
type Catalog struct {
	db *sql.DB
}

// dsnSuffix — соединительные PRAGMA в DSN: применяются драйвером к
// КАЖДОМУ соединению пула. Прежний способ (Exec в init) действовал
// только на текущее соединение: database/sql молча переоткрывает
// соединения (ошибка, вытеснение из пула), и на свежем соединении
// пропадали foreign_keys/busy_timeout. Формат параметров — контракт
// modernc.org/sqlite (doc драйвера); ':' и '\' в путях не конфликтуют
// с разбором: DSN режется по первому '?'.
const dsnSuffix = "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"

// New открывает (или создаёт) каталог по пути path; PRAGMA соединения
// заданы в DSN (busy_timeout, foreign_keys, journal_mode=WAL),
// применяются миграции и схема. Спец-путь ":memory:" — база в памяти
// (для тестов).
func New(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", path+dsnSuffix)
	if err != nil {
		return nil, fmt.Errorf("sqlite: открытие %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	c := &Catalog{db: db}
	if err := c.init(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("sqlite: закрытие после сбоя: %w", closeErr))
		}
		return nil, err
	}
	return c, nil
}

// init применяет миграции и схему (см. doc пакета и SPECIFICATION §3.1).
func (c *Catalog) init() error {
	if err := c.applyMigrations(); err != nil {
		return err
	}
	return c.applySchema()
}

// RegisterTape вставляет запись о кассете или обновляет существующую
// (UPSERT по uuid) при повторном форматировании.
func (c *Catalog) RegisterTape(ctx context.Context, uuid, name string, formattedAt int64) error {
	if _, err := c.db.ExecContext(ctx, insertTapeSQL, uuid, name, formattedAt); err != nil {
		return fmt.Errorf("sqlite: регистрация кассеты %s: %w", uuid, err)
	}
	return nil
}

// GetTapeByUUID возвращает кассету по UUID; *domain.TapeNotFoundError,
// если кассеты нет.
func (c *Catalog) GetTapeByUUID(ctx context.Context, uuid string) (port.TapeRecord, error) {
	var rec port.TapeRecord
	err := c.db.QueryRowContext(ctx, selectTapeSQL, uuid).Scan(&rec.UUID, &rec.Name, &rec.FormattedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return port.TapeRecord{}, &domain.TapeNotFoundError{UUID: uuid}
	}
	if err != nil {
		return port.TapeRecord{}, fmt.Errorf("sqlite: чтение кассеты %s: %w", uuid, err)
	}
	return rec, nil
}

// CreateSession вставляет запись о сессии и возвращает её PK.
// Part < 1 нормализуется в 1 (обычная не разделённая сессия).
func (c *Catalog) CreateSession(ctx context.Context, sess domain.Session) (int64, error) {
	if sess.Part < 1 {
		sess.Part = 1
	}
	res, err := c.db.ExecContext(ctx, insertSessionSQL,
		sess.TapeUUID, sess.Num, string(sess.Type), sess.Timestamp, sess.JobRunID, sess.Part)
	if err != nil {
		return 0, fmt.Errorf("sqlite: создание сессии %d на кассете %s: %w",
			sess.Num, sess.TapeUUID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: id созданной сессии: %w", err)
	}
	return id, nil
}

// LastSessionNum — MAX(session_num) по кассете; 0, если сессий нет.
func (c *Catalog) LastSessionNum(ctx context.Context, tapeUUID string) (int32, error) {
	var num int32
	if err := c.db.QueryRowContext(ctx, selectLastSessionNumSQL, tapeUUID).Scan(&num); err != nil {
		return 0, fmt.Errorf("sqlite: MAX(session_num) кассеты %s: %w", tapeUUID, err)
	}
	return num, nil
}

// SaveFiles пакетно вставляет файлы сессии в одной транзакции:
// при ошибке ничего не вставляется.
func (c *Catalog) SaveFiles(ctx context.Context, sessionID int64, files []domain.FileMeta) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: транзакция для файлов сессии %d: %w", sessionID, err)
	}
	stmt, err := tx.PrepareContext(ctx, insertFileSQL)
	if err != nil {
		return rollback(tx, fmt.Errorf("sqlite: подготовка вставки файлов: %w", err))
	}
	for i := range files {
		f := &files[i]
		if _, err := stmt.ExecContext(ctx,
			sessionID, f.Path, f.Size, f.ModTime, f.IsDir, f.Hash, string(f.State), f.Type, f.Linkname); err != nil {
			return rollback(tx, fmt.Errorf("sqlite: вставка файла %q (сессия %d): %w", f.Path, sessionID, err))
		}
	}
	if err := stmt.Close(); err != nil {
		return rollback(tx, fmt.Errorf("sqlite: закрытие statement: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: фиксация файлов сессии %d: %w", sessionID, err)
	}
	return nil
}

// GetLatestFileStates — карта path→FileMeta по самой поздней сессии
// каждого из путей. Запрос бьётся на пакеты: у SQLite есть лимит
// параметров в одном выражении.
func (c *Catalog) GetLatestFileStates(ctx context.Context, paths []string) (map[string]domain.FileMeta, error) {
	states := make(map[string]domain.FileMeta, len(paths))
	const batchSize = 500
	for start := 0; start < len(paths); start += batchSize {
		end := min(start+batchSize, len(paths))
		if err := c.latestStatesBatch(ctx, paths[start:end], states); err != nil {
			return nil, err
		}
	}
	return states, nil
}

// latestStatesBatch дополняет states последними состояниями для одного
// пакета путей (не более batchSize).
func (c *Catalog) latestStatesBatch(ctx context.Context, paths []string, states map[string]domain.FileMeta) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(paths)), ",")
	query := fmt.Sprintf(selectLatestStatesSQL, placeholders)
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = p
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: запрос последних состояний: %w", err)
	}
	return scanAll(rows, func(rows *sql.Rows) error {
		var fm domain.FileMeta
		var state, typ string
		if err := rows.Scan(&fm.Path, &fm.Size, &fm.ModTime, &fm.IsDir, &fm.Hash, &state, &typ, &fm.Linkname); err != nil {
			return fmt.Errorf("sqlite: чтение строки состояния: %w", err)
		}
		fm.State = domain.FileState(state)
		fm.Type = domain.FileType(typ)
		// Строки упорядочены по пути и убыванию времени: первая копия
		// пути — самая поздняя сессия.
		if _, seen := states[fm.Path]; !seen {
			states[fm.Path] = fm
		}
		return nil
	})
}

// ListTapes — все кассеты каталога, по имени.
func (c *Catalog) ListTapes(ctx context.Context) ([]port.TapeRecord, error) {
	rows, err := c.db.QueryContext(ctx, selectTapesSQL)
	if err != nil {
		return nil, fmt.Errorf("sqlite: список кассет: %w", err)
	}
	var tapes []port.TapeRecord
	scanErr := scanAll(rows, func(rows *sql.Rows) error {
		var rec port.TapeRecord
		if err := rows.Scan(&rec.UUID, &rec.Name, &rec.FormattedAt); err != nil {
			return fmt.Errorf("sqlite: чтение кассеты: %w", err)
		}
		tapes = append(tapes, rec)
		return nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return tapes, nil
}

// ListSessions — сессии; tapeUUID == "" — по всем кассетам.
func (c *Catalog) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if tapeUUID == "" {
		rows, err = c.db.QueryContext(ctx, selectAllSessionsSQL)
	} else {
		rows, err = c.db.QueryContext(ctx, selectSessionsSQL, tapeUUID)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: список сессий: %w", err)
	}
	var sessions []domain.Session
	scanErr := scanAll(rows, func(rows *sql.Rows) error {
		sess, err := scanSession(rows)
		if err != nil {
			return err
		}
		sessions = append(sessions, sess)
		return nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return sessions, nil
}

// GetSessionChain — все сессии запуска jobRunID (части цепочки
// spanning-бекапа) по возрастанию part, внутри части — по tape/num.
// Неизвестный JobRunID — пустой срез, не ошибка.
func (c *Catalog) GetSessionChain(ctx context.Context, jobRunID string) ([]domain.Session, error) {
	rows, err := c.db.QueryContext(ctx, selectSessionChainSQL, jobRunID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: цепочка запуска %s: %w", jobRunID, err)
	}
	var sessions []domain.Session
	scanErr := scanAll(rows, func(rows *sql.Rows) error {
		sess, err := scanSession(rows)
		if err != nil {
			return err
		}
		sessions = append(sessions, sess)
		return nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return sessions, nil
}

// scanSession читает строку сессии: колонки в порядке selectSessionsSQL.
func scanSession(rows *sql.Rows) (domain.Session, error) {
	var sess domain.Session
	if err := rows.Scan(&sess.ID, &sess.TapeUUID, &sess.Num,
		&sess.Type, &sess.Timestamp, &sess.JobRunID, &sess.Part); err != nil {
		return domain.Session{}, fmt.Errorf("sqlite: чтение сессии: %w", err)
	}
	return sess, nil
}

// GetFilesBySession — все файлы сессии; *domain.SessionNotFoundError,
// если сессии нет.
func (c *Catalog) GetFilesBySession(ctx context.Context, sessionID int64) ([]domain.FileMeta, error) {
	var exists bool
	if err := c.db.QueryRowContext(ctx, sessionExistsSQL, sessionID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("sqlite: проверка сессии %d: %w", sessionID, err)
	}
	if !exists {
		return nil, &domain.SessionNotFoundError{SessionID: sessionID}
	}
	rows, err := c.db.QueryContext(ctx, selectFilesBySessionSQL, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: файлы сессии %d: %w", sessionID, err)
	}
	var files []domain.FileMeta
	scanErr := scanAll(rows, func(rows *sql.Rows) error {
		var fm domain.FileMeta
		var state, typ string
		if err := rows.Scan(&fm.Path, &fm.Size, &fm.ModTime, &fm.IsDir, &fm.Hash, &state, &typ, &fm.Linkname); err != nil {
			return fmt.Errorf("sqlite: чтение файла сессии %d: %w", sessionID, err)
		}
		fm.State = domain.FileState(state)
		fm.Type = domain.FileType(typ)
		files = append(files, fm)
		return nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return files, nil
}

// GetAllFileCopies — все копии пути по всем сессиям, новые сверху.
func (c *Catalog) GetAllFileCopies(ctx context.Context, path string) ([]port.FileCopy, error) {
	return c.fileCopies(ctx, selectFileCopiesSQL, "копии пути "+path, path)
}

// SearchFiles — глобальный поиск по подстроке в пути файла.
func (c *Catalog) SearchFiles(ctx context.Context, pattern string) ([]port.FileCopy, error) {
	return c.fileCopies(ctx, searchFilesSQL, "поиск "+pattern, "%"+escapeLike(pattern)+"%")
}

// fileCopies выполняет query копий файлов и сканирует результат.
func (c *Catalog) fileCopies(ctx context.Context, query, desc string, args ...any) ([]port.FileCopy, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %s: %w", desc, err)
	}
	var copies []port.FileCopy
	scanErr := scanAll(rows, func(rows *sql.Rows) error {
		var fc port.FileCopy
		var state, typ string
		if err := rows.Scan(&fc.Meta.Path, &fc.Meta.Size, &fc.Meta.ModTime,
			&fc.Meta.IsDir, &fc.Meta.Hash, &state, &typ, &fc.Meta.Linkname,
			&fc.SessionID, &fc.SessionNum, &fc.TapeUUID, &fc.Timestamp); err != nil {
			return fmt.Errorf("sqlite: %s: чтение строки: %w", desc, err)
		}
		fc.Meta.State = domain.FileState(state)
		fc.Meta.Type = domain.FileType(typ)
		copies = append(copies, fc)
		return nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return copies, nil
}

// DeleteSession удаляет сессию и её файлы (каскадом);
// *domain.SessionNotFoundError, если сессии нет.
func (c *Catalog) DeleteSession(ctx context.Context, sessionID int64) error {
	res, err := c.db.ExecContext(ctx, deleteSessionSQL, sessionID)
	if err != nil {
		return fmt.Errorf("sqlite: удаление сессии %d: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: удаление сессии %d: %w", sessionID, err)
	}
	if n == 0 {
		return &domain.SessionNotFoundError{SessionID: sessionID}
	}
	return nil
}

// PruneSessions удаляет сессии старше before (Unix-секунды) и возвращает
// число удалённых; файлы чистятся каскадом.
func (c *Catalog) PruneSessions(ctx context.Context, before int64) (int64, error) {
	res, err := c.db.ExecContext(ctx, pruneSessionsSQL, before)
	if err != nil {
		return 0, fmt.Errorf("sqlite: очистка сессий до %d: %w", before, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: очистка сессий до %d: %w", before, err)
	}
	return n, nil
}

// Close закрывает соединение с БД.
func (c *Catalog) Close() error {
	return c.db.Close()
}

// rollback откатывает транзакцию, присоединяя ошибку отката к cause.
func rollback(tx *sql.Tx, cause error) error {
	if err := tx.Rollback(); err != nil {
		return errors.Join(cause, fmt.Errorf("sqlite: откат транзакции: %w", err))
	}
	return cause
}

// scanAll читает rows до конца, вызывая fn для каждой строки, и
// закрывает курсор, присоединяя ошибки сканирования/закрытия.
func scanAll(rows *sql.Rows, fn func(*sql.Rows) error) error {
	for rows.Next() {
		if err := fn(rows); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

// escapeLike экранирует спецсимволы LIKE (%, _, \) в подстроке поиска.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
