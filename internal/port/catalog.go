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

// Порт каталога: CRUD над кассетами, сессиями и файлами.
// См. docs/SPECIFICATION.md §3.1–3.2 (схема БД и контракт).

package port

import (
	"context"

	"lentovodec/internal/domain"
)

// TapeRecord — запись о кассете в каталоге (проекция таблицы tapes).
type TapeRecord struct {
	UUID        string // PK
	Name        string // уникальное имя кассеты
	FormattedAt int64  // Unix-секунды форматирования
}

// FileCopy — копия файла в конкретной сессии; результат
// GetAllFileCopies и SearchFiles.
type FileCopy struct {
	Meta       domain.FileMeta // метаданные файла в этой сессии
	SessionID  int64           // PK сессии
	SessionNum int32           // номер сессии на своей ленте
	TapeUUID   string          // кассета, на которой лежит копия
	Timestamp  int64           // Unix-секунды старта сессии
}

// Catalog — хранилище метаданных кассет, сессий и файлов.
// Реализация: adapter/sqlite (PRAGMA foreign_keys=ON, journal_mode=WAL).
type Catalog interface {
	// RegisterTape вставляет запись о кассете или обновляет
	// существующую (UPSERT); вызывается при форматировании.
	RegisterTape(ctx context.Context, uuid, name string, formattedAt int64) error

	// GetTapeByUUID возвращает кассету по UUID (для сверки с ярлыком
	// на ленте); ошибка, если кассета не найдена.
	GetTapeByUUID(ctx context.Context, uuid string) (TapeRecord, error)

	// CreateSession вставляет запись о сессии и возвращает её PK.
	CreateSession(ctx context.Context, sess domain.Session) (int64, error)

	// LastSessionNum — MAX(session_num) по кассете; 0, если сессий нет.
	LastSessionNum(ctx context.Context, tapeUUID string) (int32, error)

	// SaveFiles пакетно вставляет файлы сессии в одной транзакции.
	SaveFiles(ctx context.Context, sessionID int64, files []domain.FileMeta) error

	// GetLatestFileStates — карта path→FileMeta по самой поздней
	// сессии каждого пути (пакетный запрос Scanner для mirror).
	GetLatestFileStates(ctx context.Context, paths []string) (map[string]domain.FileMeta, error)

	// ListTapes — все кассеты каталога.
	ListTapes(ctx context.Context) ([]TapeRecord, error)

	// ListSessions — сессии; tapeUUID == "" — по всем кассетам.
	ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error)

	// GetSessionChain — все сессии запуска jobRunID (части цепочки
	// spanning-бекапа), упорядоченные по part, внутри части — по
	// tape/num. Пустой срез для неизвестного JobRunID (не ошибка).
	GetSessionChain(ctx context.Context, jobRunID string) ([]domain.Session, error)

	// GetFilesBySession — все файлы сессии; ошибка, если сессии нет.
	GetFilesBySession(ctx context.Context, sessionID int64) ([]domain.FileMeta, error)

	// GetAllFileCopies — все копии пути во всех сессиях, упорядоченные
	// по убыванию времени (для smart restore).
	GetAllFileCopies(ctx context.Context, path string) ([]FileCopy, error)

	// SearchFiles — глобальный поиск по подстроке в пути файла.
	SearchFiles(ctx context.Context, pattern string) ([]FileCopy, error)

	// DeleteSession удаляет сессию и её файлы (каскадом).
	DeleteSession(ctx context.Context, sessionID int64) error

	// PruneSessions удаляет сессии старше before (Unix-секунды)
	// и возвращает число удалённых.
	PruneSessions(ctx context.Context, before int64) (int64, error)

	// Close закрывает соединение с БД.
	Close() error
}
