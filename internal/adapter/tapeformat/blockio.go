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

// Байтовый мост между лентой (port.Tape) и потоковым I/O (io.Reader/Writer).

package tapeformat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// tapeBlockWriter адаптирует ленту под io.Writer: копит байты в буфер
// размером domain.BlockSize и пишет цельными блоками; flush добивает
// остаток нулями до полного блока.
type tapeBlockWriter struct {
	ctx  context.Context
	tape port.Tape
	buf  [domain.BlockSize]byte
	n    int
	err  error // первая ошибка; залипает (sticky)
}

// Write реализует io.Writer.
func (w *tapeBlockWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 && w.err == nil {
		n := copy(w.buf[w.n:], p)
		w.n += n
		p = p[n:]
		written += n
		if w.n == domain.BlockSize {
			w.writeBlock()
		}
	}
	return written, w.err
}

// flush дописывает неполный блок (добитый нулями) и возвращает первую
// накопленную ошибку.
func (w *tapeBlockWriter) flush() error {
	if w.err == nil && w.n > 0 {
		w.writeBlock()
	}
	return w.err
}

// writeBlock сбрасывает буфер на ленту как один блок.
func (w *tapeBlockWriter) writeBlock() {
	if err := w.tape.WriteBlock(w.ctx, w.buf[:]); err != nil {
		w.err = fmt.Errorf("tapeformat: запись блока: %w", err)
	}
	w.n = 0
}

// tapeBlockReader адаптирует ленту под io.Reader: выдаёт содержимое
// блоков подряд; filemark/EOD превращаются в io.EOF.
type tapeBlockReader struct {
	ctx  context.Context
	tape port.Tape
	cur  []byte // остаток текущего блока
	done bool   // достигнут filemark/EOD
}

// Read реализует io.Reader.
func (r *tapeBlockReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		if r.done {
			return 0, io.EOF
		}
		block, err := r.tape.ReadBlock(r.ctx)
		if errors.Is(err, io.EOF) {
			r.done = true
			return 0, io.EOF
		}
		if err != nil {
			return 0, fmt.Errorf("tapeformat: чтение блока: %w", err)
		}
		r.cur = block
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

// progressOr заменяет nil-прогресс на заглушку.
func progressOr(p port.ProgressReporter) port.ProgressReporter {
	if p == nil {
		return discardProgress{}
	}
	return p
}

// discardProgress — внутренняя заглушка port.ProgressReporter.
type discardProgress struct{}

// Update отбрасывает снимок прогресса.
func (discardProgress) Update(port.ProgressUpdate) {}

// Done — no-op.
func (discardProgress) Done() {}

// Fail — no-op.
func (discardProgress) Fail(error) {}

// copyFromReader копирует r в w буфером buf, публикуя прогресс после
// каждого чанка. Останавливается на EOF, ошибке или отмене контекста.
func copyFromReader(
	ctx context.Context,
	r io.Reader,
	w io.Writer,
	buf []byte,
	prog port.ProgressReporter,
	processed *int64,
	total int64,
	current string,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("tapeformat: копирование %q: %w", current, err)
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			*processed += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("tapeformat: запись %q: %w", current, werr)
			}
			prog.Update(port.ProgressUpdate{
				Phase:          port.PhaseWrite,
				CurrentFile:    current,
				ProcessedBytes: *processed,
				TotalBytes:     total,
			})
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("tapeformat: чтение %q: %w", current, rerr)
		}
	}
}
