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

//go:build tape && linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || s390x)

// Реализация port.Tape поверх LTO-стримера через драйвер st (/dev/nst*).
// Устройство должно работать в fixed-block режиме с размером блока
// domain.BlockSize (устанавливается mt setblk / MTSETBLK; обычно
// настраивается при форматировании кассеты).

package linuxtape

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"lentovodec/internal/domain"
)

// Tape — port.Tape на реальном стримере. Не потокобезопасен: лента —
// физически последовательное устройство, одновременные операции
// бессмысленны; вызывайте из одной горутины (usecase так и делает).
type Tape struct {
	f *os.File
}

// Open открывает устройство (обычно /dev/nst0). Доступ — rootless через
// группу tape (docs/SPECIFICATION.md §9.1): при EACCES сюда приходит
// обычная *os.PathError, iface/cli превратит её в подсказку.
func Open(path string) (*Tape, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("linuxtape: открытие %s: %w", path, err)
	}
	return &Tape{f: f}, nil
}

// ReadBlock читает один блок фиксированного размера. Чтение поверх
// filemark'а возвращает 0 байт — это io.EOF по контракту port.Tape;
// следующий ReadBlock начнёт следующий файл.
func (t *Tape) ReadBlock(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buf := make([]byte, domain.BlockSize)
	// ioctl заблокировать нельзя; отмена проверяется до обмена.
	if _, err := io.ReadFull(t.f, buf); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("linuxtape: чтение блока: %w", err)
	}
	return buf, nil
}

// WriteBlock записывает блок. Блок короче domain.BlockSize добивается
// нулями, длиннее — ошибка. Переполнение ленты всплывает как ENOSPC
// (*os.PathError); маппинг в domain.TapeFullError — задача use case.
func (t *Tape) WriteBlock(ctx context.Context, block []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(block) > domain.BlockSize {
		return fmt.Errorf("linuxtape: блок %d байт больше BlockSize=%d", len(block), domain.BlockSize)
	}
	buf := make([]byte, domain.BlockSize)
	copy(buf, block)
	if _, err := t.f.Write(buf); err != nil {
		return fmt.Errorf("linuxtape: запись блока: %w", err)
	}
	return nil
}

// WriteEOF пишет filemark (MTWEOF).
func (t *Tape) WriteEOF(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendTapeCommand(t.f.Fd(), mtWEOF, 1)
}

// ForwardFilemarks пропускает n filemark'ов вперёд (MTFSF): позиция —
// на первой записи после n-го filemark'а.
func (t *Tape) ForwardFilemarks(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendTapeCommand(t.f.Fd(), mtFSF, int32(n))
}

// BackwardFilemarks пропускает n filemark'ов назад (MTBSFM): позиция —
// сразу после n-го filemark'а, в начале следующей за ним записи.
func (t *Tape) BackwardFilemarks(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendTapeCommand(t.f.Fd(), mtBSFM, int32(n))
}

// Rewind перематывает ленту в начало (MTREW).
func (t *Tape) Rewind(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendTapeCommand(t.f.Fd(), mtREW, 1)
}

// EndOfData перемещает ленту в конец данных (MTEOM) — позицию дозаписи.
func (t *Tape) EndOfData(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendTapeCommand(t.f.Fd(), mtEOM, 1)
}

// Eject перематывает и извлекает кассету (MTOFFL).
func (t *Tape) Eject(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendTapeCommand(t.f.Fd(), mtOFFL, 1)
}

// Close закрывает дескриптор устройства.
func (t *Tape) Close() error {
	return t.f.Close()
}
