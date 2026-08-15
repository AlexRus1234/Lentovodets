// Фасад пакета, реализующий port.TapeCodec поверх публичных функций.

package tapeformat

import (
	"context"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// Codec реализует port.TapeCodec; без состояния.
type Codec struct{}

// NewCodec создаёт кодек ленты.
func NewCodec() *Codec {
	return &Codec{}
}

// EncodeLabel кодирует ярлык в блок domain.BlockSize.
func (Codec) EncodeLabel(label domain.TapeLabel) ([]byte, error) {
	return EncodeLabel(label)
}

// DecodeLabel разбирает блок ярлыка.
func (Codec) DecodeLabel(block []byte) (domain.TapeLabel, error) {
	return DecodeLabel(block)
}

// WriteSession пишет сессию в текущую позицию ленты.
func (Codec) WriteSession(
	ctx context.Context,
	tape port.Tape,
	header port.SessionHeader,
	files []domain.FileMeta,
	fs port.FileReader,
	prog port.ProgressReporter,
) error {
	return WriteSession(ctx, tape, SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    header.SessionNum,
		Type:          header.Type,
		JobRunID:      header.JobRunID,
		Timestamp:     header.Timestamp,
		JobName:       header.JobName,
		Files:         files,
	}, fs, prog)
}

// ReadSession читает сессию с текущей позиции ленты; dest == nil —
// режим проверки.
func (Codec) ReadSession(
	ctx context.Context,
	tape port.Tape,
	dest port.FileWriter,
	prog port.ProgressReporter,
) ([]domain.FileMeta, error) {
	return ReadSession(ctx, tape, dest, prog)
}
