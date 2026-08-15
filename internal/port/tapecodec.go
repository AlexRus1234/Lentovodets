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
	// режим проверки без записи на ФС.
	ReadSession(ctx context.Context, tape Tape, dest FileWriter,
		prog ProgressReporter) ([]domain.FileMeta, error)
}
