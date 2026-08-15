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

	insertSessionSQL = `INSERT INTO sessions (tape_uuid, session_num, type, timestamp, job_run_id)
		VALUES (?, ?, ?, ?, ?)`

	selectLastSessionNumSQL = `SELECT COALESCE(MAX(session_num), 0) FROM sessions WHERE tape_uuid = ?`

	insertFileSQL = `INSERT INTO files (session_id, path, size, mod_time, is_dir, hash, state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	selectLatestStatesSQL = `SELECT f.path, f.size, f.mod_time, f.is_dir, f.hash, f.state
		FROM files f
		JOIN sessions s ON s.id = f.session_id
		WHERE f.path IN (%s)
		ORDER BY f.path ASC, s.timestamp DESC, s.id DESC`

	selectTapesSQL = `SELECT uuid, name, formatted_at FROM tapes ORDER BY name`

	selectSessionsSQL = `SELECT id, tape_uuid, session_num, type, timestamp, job_run_id
		FROM sessions WHERE tape_uuid = ? ORDER BY session_num`

	selectAllSessionsSQL = `SELECT id, tape_uuid, session_num, type, timestamp, job_run_id
		FROM sessions ORDER BY tape_uuid, session_num`

	sessionExistsSQL = `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`

	selectFilesBySessionSQL = `SELECT path, size, mod_time, is_dir, hash, state
		FROM files WHERE session_id = ? ORDER BY path`

	selectFileCopiesSQL = `SELECT f.path, f.size, f.mod_time, f.is_dir, f.hash, f.state,
			s.id, s.session_num, s.tape_uuid, s.timestamp
		FROM files f
		JOIN sessions s ON s.id = f.session_id
		WHERE f.path = ?
		ORDER BY s.timestamp DESC, s.id DESC`

	searchFilesSQL = `SELECT f.path, f.size, f.mod_time, f.is_dir, f.hash, f.state,
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

// New открывает (или создаёт) каталог по пути path, включает PRAGMA
// foreign_keys / journal_mode=WAL / busy_timeout и применяет схему.
// Спец-путь ":memory:" — база в памяти (для тестов).
func New(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", path)
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

// init применяет PRAGMA и схему (см. doc пакета и SPECIFICATION §3.1).
func (c *Catalog) init() error {
	pragmas := [...]string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, p := range pragmas {
		if _, err := c.db.Exec(p); err != nil {
			return fmt.Errorf("sqlite: %s: %w", p, err)
		}
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
func (c *Catalog) CreateSession(ctx context.Context, sess domain.Session) (int64, error) {
	res, err := c.db.ExecContext(ctx, insertSessionSQL,
		sess.TapeUUID, sess.Num, string(sess.Type), sess.Timestamp, sess.JobRunID)
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
			sessionID, f.Path, f.Size, f.ModTime, f.IsDir, f.Hash, string(f.State)); err != nil {
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
		var state string
		if err := rows.Scan(&fm.Path, &fm.Size, &fm.ModTime, &fm.IsDir, &fm.Hash, &state); err != nil {
			return fmt.Errorf("sqlite: чтение строки состояния: %w", err)
		}
		fm.State = domain.FileState(state)
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
		var sess domain.Session
		if err := rows.Scan(&sess.ID, &sess.TapeUUID, &sess.Num,
			&sess.Type, &sess.Timestamp, &sess.JobRunID); err != nil {
			return fmt.Errorf("sqlite: чтение сессии: %w", err)
		}
		sessions = append(sessions, sess)
		return nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return sessions, nil
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
		var state string
		if err := rows.Scan(&fm.Path, &fm.Size, &fm.ModTime, &fm.IsDir, &fm.Hash, &state); err != nil {
			return fmt.Errorf("sqlite: чтение файла сессии %d: %w", sessionID, err)
		}
		fm.State = domain.FileState(state)
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
		var state string
		if err := rows.Scan(&fc.Meta.Path, &fc.Meta.Size, &fc.Meta.ModTime,
			&fc.Meta.IsDir, &fc.Meta.Hash, &state,
			&fc.SessionID, &fc.SessionNum, &fc.TapeUUID, &fc.Timestamp); err != nil {
			return fmt.Errorf("sqlite: %s: чтение строки: %w", desc, err)
		}
		fc.Meta.State = domain.FileState(state)
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
