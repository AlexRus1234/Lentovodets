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

// MemCatalog — in-memory реализация port.Catalog для use case-тестов.
// См. docs/TESTING.md §3.3. Семантика повторяет adapter/sqlite: те же
// типизированные ошибки и порядки сортировки.

package testutil

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// MemCatalog хранит кассеты, сессии и файлы в map'ах под мьютексом.
type MemCatalog struct {
	mu       sync.Mutex
	nextID   int64
	tapes    map[string]port.TapeRecord
	sessions map[int64]domain.Session
	files    map[int64][]domain.FileMeta // sessionID → файлы по порядку вставки
}

// NewMemCatalog создаёт пустой каталог.
func NewMemCatalog() *MemCatalog {
	return &MemCatalog{
		tapes:    make(map[string]port.TapeRecord),
		sessions: make(map[int64]domain.Session),
		files:    make(map[int64][]domain.FileMeta),
	}
}

// WithSnapshot засеивает прошлый снимок: регистрирует кассету
// snapshot-tape с одной FULL-сессией и перечисленными файлами.
// Подготавливает состояние для Scanner.Scan (GetLatestFileStates).
func (c *MemCatalog) WithSnapshot(files map[string]domain.FileMeta) *MemCatalog {
	ctx := context.Background()
	// Ошибок быть не может: кассета уникальна, файлов немного.
	_ = c.RegisterTape(ctx, "snapshot-tape", "snapshot", 0)
	id, _ := c.CreateSession(ctx, domain.Session{
		TapeUUID:  "snapshot-tape",
		Num:       1,
		Type:      domain.SessionFull,
		Timestamp: 1,
		JobRunID:  "snapshot-run",
	})
	sorted := make([]domain.FileMeta, 0, len(files))
	for _, fm := range files {
		sorted = append(sorted, fm)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	_ = c.SaveFiles(ctx, id, sorted)
	return c
}

// RegisterTape вставляет запись о кассете или обновляет существующую
// (UPSERT по uuid).
func (c *MemCatalog) RegisterTape(ctx context.Context, uuid, name string, formattedAt int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tapes[uuid] = port.TapeRecord{UUID: uuid, Name: name, FormattedAt: formattedAt}
	return nil
}

// GetTapeByUUID возвращает кассету по UUID; *domain.TapeNotFoundError,
// если кассеты нет.
func (c *MemCatalog) GetTapeByUUID(ctx context.Context, uuid string) (port.TapeRecord, error) {
	if err := ctx.Err(); err != nil {
		return port.TapeRecord{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.tapes[uuid]
	if !ok {
		return port.TapeRecord{}, &domain.TapeNotFoundError{UUID: uuid}
	}
	return rec, nil
}

// CreateSession вставляет запись о сессии и возвращает её PK;
// Part < 1 нормализуется в 1 (как в sqlite); ошибка FK, если кассета
// не зарегистрирована (как при PRAGMA foreign_keys=ON в sqlite).
func (c *MemCatalog) CreateSession(ctx context.Context, sess domain.Session) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if sess.Part < 1 {
		sess.Part = 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tapes[sess.TapeUUID]; !ok {
		return 0, fmt.Errorf("memcatalog: FK: кассета %s не зарегистрирована", sess.TapeUUID)
	}
	c.nextID++
	id := c.nextID
	sess.ID = id
	c.sessions[id] = sess
	return id, nil
}

// LastSessionNum — MAX(session_num) по кассете; 0, если сессий нет.
func (c *MemCatalog) LastSessionNum(ctx context.Context, tapeUUID string) (int32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var max int32
	for _, sess := range c.sessions {
		if sess.TapeUUID == tapeUUID && sess.Num > max {
			max = sess.Num
		}
	}
	return max, nil
}

// SaveFiles пакетно вставляет файлы сессии (все или ничего).
func (c *MemCatalog) SaveFiles(ctx context.Context, sessionID int64, files []domain.FileMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.sessions[sessionID]; !ok {
		return fmt.Errorf("memcatalog: FK: сессия %d не существует", sessionID)
	}
	if len(files) == 0 {
		return nil
	}
	c.files[sessionID] = append(c.files[sessionID], files...)
	return nil
}

// GetLatestFileStates — карта path→FileMeta по самой поздней сессии
// каждого пути (позже = больший timestamp, при равных — больший ID).
func (c *MemCatalog) GetLatestFileStates(ctx context.Context, paths []string) (map[string]domain.FileMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	states := make(map[string]domain.FileMeta, len(paths))
	for _, p := range paths {
		var (
			best     domain.FileMeta
			bestSess domain.Session
			found    bool
		)
		for id, files := range c.files {
			for _, fm := range files {
				if fm.Path != p {
					continue
				}
				sess := c.sessions[id]
				if !found || compareSessions(sess, bestSess) {
					best, bestSess, found = fm, sess, true
				}
			}
		}
		if found {
			states[p] = best
		}
	}
	return states, nil
}

// compareSessions сообщает, что сессия a новее b: больший timestamp,
// при равенстве — больший ID.
func compareSessions(a, b domain.Session) bool {
	if a.Timestamp != b.Timestamp {
		return a.Timestamp > b.Timestamp
	}
	return a.ID > b.ID
}

// ListTapes — все кассеты каталога, по имени.
func (c *MemCatalog) ListTapes(ctx context.Context) ([]port.TapeRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tapes := make([]port.TapeRecord, 0, len(c.tapes))
	for _, rec := range c.tapes {
		tapes = append(tapes, rec)
	}
	sort.Slice(tapes, func(i, j int) bool { return tapes[i].Name < tapes[j].Name })
	return tapes, nil
}

// ListSessions — сессии; tapeUUID == "" — по всем кассетам.
func (c *MemCatalog) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sessions := make([]domain.Session, 0, len(c.sessions))
	for _, sess := range c.sessions {
		if tapeUUID == "" || sess.TapeUUID == tapeUUID {
			sessions = append(sessions, sess)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].TapeUUID != sessions[j].TapeUUID {
			return sessions[i].TapeUUID < sessions[j].TapeUUID
		}
		return sessions[i].Num < sessions[j].Num
	})
	return sessions, nil
}

// GetSessionChain — все сессии запуска jobRunID (части цепочки
// spanning-бекапа) по возрастанию part, внутри части — по tape/num.
// Неизвестный JobRunID — пустой срез, не ошибка. Сортировка
// эквивалентна ORDER BY part, tape_uuid, session_num в sqlite.
func (c *MemCatalog) GetSessionChain(ctx context.Context, jobRunID string) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sessions := make([]domain.Session, 0, len(c.sessions))
	for _, sess := range c.sessions {
		if sess.JobRunID == jobRunID {
			sessions = append(sessions, sess)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Part != sessions[j].Part {
			return sessions[i].Part < sessions[j].Part
		}
		if sessions[i].TapeUUID != sessions[j].TapeUUID {
			return sessions[i].TapeUUID < sessions[j].TapeUUID
		}
		return sessions[i].Num < sessions[j].Num
	})
	return sessions, nil
}

// GetFilesBySession — все файлы сессии по пути; *domain.SessionNotFoundError,
// если сессии нет.
func (c *MemCatalog) GetFilesBySession(ctx context.Context, sessionID int64) ([]domain.FileMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.sessions[sessionID]; !ok {
		return nil, &domain.SessionNotFoundError{SessionID: sessionID}
	}
	files := append([]domain.FileMeta(nil), c.files[sessionID]...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// GetAllFileCopies — все копии пути во всех сессиях, новые сверху.
func (c *MemCatalog) GetAllFileCopies(ctx context.Context, path string) ([]port.FileCopy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.copies(func(fm domain.FileMeta) bool { return fm.Path == path }), nil
}

// SearchFiles — поиск по подстроке в пути файла.
func (c *MemCatalog) SearchFiles(ctx context.Context, pattern string) ([]port.FileCopy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.copies(func(fm domain.FileMeta) bool {
		return strings.Contains(fm.Path, pattern)
	}), nil
}

// copies собирает копии файлов по фильтру: по пути, затем новые сверху.
func (c *MemCatalog) copies(match func(domain.FileMeta) bool) []port.FileCopy {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []port.FileCopy
	for id, files := range c.files {
		for _, fm := range files {
			if !match(fm) {
				continue
			}
			sess := c.sessions[id]
			out = append(out, port.FileCopy{
				Meta:       fm,
				SessionID:  sess.ID,
				SessionNum: sess.Num,
				TapeUUID:   sess.TapeUUID,
				Timestamp:  sess.Timestamp,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Meta.Path != out[j].Meta.Path {
			return out[i].Meta.Path < out[j].Meta.Path
		}
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp > out[j].Timestamp
		}
		return out[i].SessionID > out[j].SessionID
	})
	return out
}

// DeleteSession удаляет сессию и её файлы;
// *domain.SessionNotFoundError, если сессии нет.
func (c *MemCatalog) DeleteSession(ctx context.Context, sessionID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.sessions[sessionID]; !ok {
		return &domain.SessionNotFoundError{SessionID: sessionID}
	}
	delete(c.sessions, sessionID)
	delete(c.files, sessionID)
	return nil
}

// PruneSessions удаляет сессии старше before (Unix-секунды) и
// возвращает число удалённых.
func (c *MemCatalog) PruneSessions(ctx context.Context, before int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var removed int64
	for id, sess := range c.sessions {
		if sess.Timestamp < before {
			delete(c.sessions, id)
			delete(c.files, id)
			removed++
		}
	}
	return removed, nil
}

// Close — no-op (in-memory).
func (c *MemCatalog) Close() error { return nil }
