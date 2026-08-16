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

// Порт кодека ленты: ярлык и сессии. Реализация — adapter/tapeformat;
// wire-представление (JSON-индекс, tar) остаётся внутри адаптера.

package port

import (
	"context"

	"lentovodec/internal/domain"
)

// SessionHeader — метаданные сессии для индекса на ленте
// (docs/FORMAT.md §6), без списка файлов.
type SessionHeader struct {
	SessionNum int32              // номер сессии на ленте, начиная с 1
	Type       domain.SessionType // FULL или INC
	JobRunID   string             // UUID запуска
	Timestamp  int64              // Unix-секунды старта сессии
	JobName    string             // имя задания
	Part       int32              // номер части spanning-цепочки, с 1; 0 = 1 (старые ленты)
	Continues  string             // UUID предыдущей кассеты цепочки; "" у части 1
}

// Continuation — указатель продолжения: блок в конце кассеты,
// сообщающий, что цепочка сессий продолжается на следующей кассете
// (имя, а не UUID: UUID следующей кассеты неизвестен до её
// форматирования).
type Continuation struct {
	JobRunID     string // UUID запуска, к которому относится цепочка
	SessionNum   int32  // номер сессии на ленте
	Part         int32  // номер части, продолжающейся на следующей кассете
	NextTapeName string // имя следующей кассеты цепочки
}

// TapeCodec — кодирование ярлыка кассеты и сессий ленты.
type TapeCodec interface {
	// EncodeLabel кодирует ярлык в один блок domain.BlockSize.
	EncodeLabel(label domain.TapeLabel) ([]byte, error)

	// DecodeLabel разбирает блок ярлыка; типизированные ошибки —
	// BlankTapeError / ForeignFormatError / NewerFormatError.
	DecodeLabel(block []byte) (domain.TapeLabel, error)

	// WriteSession пишет сессию (индекс + tar) в текущую позицию
	// ленты; позиционирование — зона вызывающего.
	WriteSession(ctx context.Context, tape Tape, header SessionHeader,
		files []domain.FileMeta, fs FileReader, prog ProgressReporter) error

	// ReadSession читает сессию с текущей позиции ленты; dest == nil —
	// режим проверки без записи на ФС. Если на позиции сессии лежит
	// блок-указатель продолжения — *domain.ContinuationError.
	ReadSession(ctx context.Context, tape Tape, dest FileWriter,
		prog ProgressReporter) ([]domain.FileMeta, error)

	// WriteContinuation пишет блок-указатель продолжения в текущую
	// позицию ленты (сразу после filemark'а tar завершённой части);
	// filemark'и не ставит — закрывающую EOD-пару (новый EOD кассеты)
	// пишет вызывающий.
	WriteContinuation(ctx context.Context, tape Tape, c Continuation) error

	// ReadContinuation читает блок-указатель с текущей позиции.
	// Блока нет (конец данных / пустой блок) — io.EOF; чужой блок —
	// *domain.NotContinuationError.
	ReadContinuation(ctx context.Context, tape Tape) (Continuation, error)
}
