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

// FakeCodec — двойник port.TapeCodec: JSON-ярлык без паддинга,
// сессии только записываются в память. См. docs/TESTING.md §3.

package testutil

import (
	"context"
	"encoding/json"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// FakeCodec запоминает вызовы; поля Err* инъектируют сбои.
// ErrReadOnce срабатывает один раз и сбрасывается (повреждение
// конкретной копии). ReadSession раздаёт сессии из очереди Queue
// по порядку; когда очередь пуста — *domain.EmptyIndexError (конец
// данных, как у реального декодера). ReadFiles используется, если
// очередь пуста и задан непустой слайс.
type FakeCodec struct {
	ErrEncode    error
	ErrDecode    error
	ErrWrite     error
	ErrRead      error
	ErrReadOnce  error // срабатывает на ErrReadOn-м вызове и сбрасывается
	ErrReadOn    int   // номер вызова ReadSession (с 1) для ErrReadOnce
	Queue        [][]domain.FileMeta
	ReadFiles    []domain.FileMeta
	WroteHeaders []port.SessionHeader
	WroteFiles   [][]domain.FileMeta
	ReadCalls    int
}

// EncodeLabel кодирует ярлык в JSON (без паддинга до BlockSize).
func (c *FakeCodec) EncodeLabel(label domain.TapeLabel) ([]byte, error) {
	if c.ErrEncode != nil {
		return nil, c.ErrEncode
	}
	return json.Marshal(label)
}

// DecodeLabel разбирает JSON-ярлык; пустой блок — BlankTapeError,
// чужой JSON/magic — ForeignFormatError, новая версия — NewerFormatError.
func (c *FakeCodec) DecodeLabel(block []byte) (domain.TapeLabel, error) {
	if c.ErrDecode != nil {
		return domain.TapeLabel{}, c.ErrDecode
	}
	trimmed := trimZeroBytes(block)
	if len(trimmed) == 0 {
		return domain.TapeLabel{}, &domain.BlankTapeError{}
	}
	var label domain.TapeLabel
	if err := json.Unmarshal(trimmed, &label); err != nil {
		return domain.TapeLabel{}, &domain.ForeignFormatError{Magic: "fake"}
	}
	if label.Magic != domain.Magic {
		return domain.TapeLabel{}, &domain.ForeignFormatError{Magic: label.Magic}
	}
	if label.FormatVersion > domain.FormatVersion {
		return domain.TapeLabel{}, &domain.NewerFormatError{
			Found: label.FormatVersion, Supported: domain.FormatVersion}
	}
	return label, nil
}

// WriteSession запоминает заголовок и файлы сессии.
func (c *FakeCodec) WriteSession(
	ctx context.Context,
	tape port.Tape,
	header port.SessionHeader,
	files []domain.FileMeta,
	fs port.FileReader,
	prog port.ProgressReporter,
) error {
	if c.ErrWrite != nil {
		return c.ErrWrite
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if prog != nil {
		prog.Update(port.ProgressUpdate{
			Phase:          port.PhaseWrite,
			ProcessedBytes: int64(len(files)),
		})
	}
	c.WroteHeaders = append(c.WroteHeaders, header)
	c.WroteFiles = append(c.WroteFiles, append([]domain.FileMeta(nil), files...))
	return nil
}

// ReadSession выдаёт очередную сессию из Queue; при пустой очереди —
// ReadFiles, если заданы, иначе *domain.EmptyIndexError.
func (c *FakeCodec) ReadSession(
	ctx context.Context,
	tape port.Tape,
	dest port.FileWriter,
	prog port.ProgressReporter,
) ([]domain.FileMeta, error) {
	c.ReadCalls++
	if c.ErrRead != nil {
		return nil, c.ErrRead
	}
	if c.ErrReadOnce != nil && c.ReadCalls == c.ErrReadOn {
		err := c.ErrReadOnce
		c.ErrReadOnce = nil
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(c.Queue) > 0 {
		files := c.Queue[0]
		c.Queue = c.Queue[1:]
		return append([]domain.FileMeta(nil), files...), nil
	}
	if len(c.ReadFiles) > 0 {
		return append([]domain.FileMeta(nil), c.ReadFiles...), nil
	}
	return nil, &domain.EmptyIndexError{}
}

// trimZeroBytes отрезает замыкающие нули.
func trimZeroBytes(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}
