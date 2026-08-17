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
		Part:          header.Part,
		Continues:     header.Continues,
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

// ReadHeader читает заголовок сессии (индекс без tar) с текущей
// позиции ленты.
func (Codec) ReadHeader(ctx context.Context, tape port.Tape) (port.SessionHeader, error) {
	return ReadHeader(ctx, tape)
}

// WriteContinuation пишет блок-указатель продолжения и filemark EOD.
func (Codec) WriteContinuation(ctx context.Context, tape port.Tape, c port.Continuation) error {
	return WriteContinuation(ctx, tape, c)
}

// ReadContinuation читает блок-указатель продолжения с текущей позиции.
func (Codec) ReadContinuation(ctx context.Context, tape port.Tape) (port.Continuation, error) {
	return ReadContinuation(ctx, tape)
}
