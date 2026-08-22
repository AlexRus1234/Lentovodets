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

// Реализация port.Tape поверх обычного файла: «лента-как-файл» для dev/CI.
//
// Файл-лента — это magic-заголовок и последовательность записей
// (фреймов): блок данных или filemark. Позиция ленты — байтовое
// смещение внутри файла; запись в середину уничтожает хвост (как
// перезапись хвоста реальной ленты). Семантика навигации повторяет
// MTFSF/MTBSFM/MTEOM (docs/FORMAT.md §9).

package filetape

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"lentovodec/internal/domain"
)

// Формат файла-ленты:
//
//	"FTAPEV1\0"                       — magic (8 байт), распознаётся при открытии
//	запись* — блок данных или filemark:
//	  0x01 | uint32le len | payload   — блок данных (len = domain.BlockSize)
//	  0x02 | uint32le 0    | —        — filemark
const (
	fileMagic   = "FTAPEV1\x00"
	hdrSize     = 5 // 1 байт тип + 4 байта LE длина payload
	recData     = 0x01
	recFilemark = 0x02
)

// Tape — port.Tape поверх файла. Один файл = одна лента. Safe for
// concurrent use: все операции под мьютексом. Открытие всегда ставит
// позицию в начало ленты (BOT), как загрузка кассеты в привод.
type Tape struct {
	mu       sync.Mutex
	f        *os.File
	pos      int64 // смещение следующей читаемой/пишемой записи
	size     int64 // логический размер данных (>= len(fileMagic))
	capacity int64 // лимит полезной ёмкости; <= 0 — без лимита
	used     int64 // расход ёмкости при capacity > 0 (актуален до size)
}

// Open открывает (или создаёт) файл-ленту по пути path. Существующий
// непустой файл должен быть файлом-лентой, иначе InvalidTapeFileError.
// Ёмкость не ограничена; для лимита см. OpenCapacity.
func Open(path string) (*Tape, error) {
	return open(path, 0)
}

// OpenCapacity открывает (или создаёт) файл-ленту с лимитом ёмкости —
// чтобы сценарии «кончилась лента» гонялись в CI без привода.
//
// Семантика лимита (в байтах полезной записи; magic-заголовок и
// заголовки фреймов файла-ленты не учитываются):
//   - блок данных занимает domain.BlockSize — блоки хранятся добитыми
//     до полного размера, как на реальной ленте;
//   - filemark занимает 1 байт.
//
// WriteBlock/WriteEOF, не влезающие в остаток ёмкости, возвращают
// ошибку, содержащую «no space left on device» (её распознаёт
// backup-слой как конец ленты), и не меняют ленту. При открытии
// существующего файла использованная ёмкость пересчитывается по его
// записям; если записей больше, чем влезает в лимит, открытие успешно,
// но любая запись будет отклонена.
func OpenCapacity(path string, capacity int64) (*Tape, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("filetape: OpenCapacity: capacity должен быть > 0, получено %d", capacity)
	}
	return open(path, capacity)
}

// open — общая реализация Open/OpenCapacity.
func open(path string, capacity int64) (*Tape, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("filetape: открытие %q: %w", path, err)
	}
	t := &Tape{f: f, capacity: capacity}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("filetape: stat %q: %w", path, err)
	}
	switch {
	case st.Size() == 0:
		if _, err := f.WriteAt([]byte(fileMagic), 0); err != nil {
			f.Close()
			return nil, fmt.Errorf("filetape: запись magic: %w", err)
		}
		t.size = int64(len(fileMagic))
	default:
		hdr := make([]byte, len(fileMagic))
		if _, err := f.ReadAt(hdr, 0); err != nil {
			f.Close()
			return nil, fmt.Errorf("filetape: чтение magic %q: %w", path, err)
		}
		if string(hdr) != fileMagic {
			f.Close()
			return nil, &InvalidTapeFileError{Path: path}
		}
		t.size = st.Size()
	}
	if capacity > 0 {
		used, err := t.usageUpTo(t.size)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("filetape: подсчёт ёмкости %q: %w", path, err)
		}
		t.used = used
	}
	t.pos = int64(len(fileMagic))
	return t, nil
}

// ReadBlock читает один блок. Filemark и конец данных — io.EOF
// (filemark потребляется, как в FakeTape и драйвере st).
func (t *Tape) ReadBlock(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pos >= t.size {
		return nil, io.EOF
	}
	typ, length, next, err := t.recordAt(t.pos)
	if err != nil {
		return nil, err
	}
	if typ == recFilemark {
		t.pos = next
		return nil, io.EOF
	}
	payload := make([]byte, length)
	if _, err := t.f.ReadAt(payload, t.pos+hdrSize); err != nil {
		return nil, fmt.Errorf("filetape: чтение блока на смещении %d: %w", t.pos, err)
	}
	t.pos = next
	return payload, nil
}

// WriteBlock записывает блок в текущую позицию, уничтожая хвост ленты.
// Блок короче domain.BlockSize добивается нулями, длиннее — ошибка.
func (t *Tape) WriteBlock(ctx context.Context, block []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(block) > domain.BlockSize {
		return fmt.Errorf("filetape: блок %d байт больше BlockSize=%d", len(block), domain.BlockSize)
	}
	buf := make([]byte, hdrSize+domain.BlockSize)
	buf[0] = recData
	binary.LittleEndian.PutUint32(buf[1:hdrSize], domain.BlockSize)
	copy(buf[hdrSize:], block)
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.appendLocked(buf)
}

// WriteEOF ставит filemark в текущую позицию, уничтожая хвост ленты.
func (t *Tape) WriteEOF(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.appendLocked([]byte{recFilemark, 0, 0, 0, 0})
}

// ForwardFilemarks пропускает n filemark'ов вперёд (MTFSF): позиция —
// сразу после n-го filemark'а, в начале следующей записи.
func (t *Tape) ForwardFilemarks(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n <= 0 {
		return fmt.Errorf("filetape: MTFSF: count должен быть > 0, получено %d", n)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	found := 0
	off := t.pos
	for off < t.size {
		typ, _, next, err := t.recordAt(off)
		if err != nil {
			return err
		}
		if typ == recFilemark {
			found++
			if found == n {
				t.pos = next
				return nil
			}
		}
		off = next
	}
	return fmt.Errorf("filetape: MTFSF(%d): вперёд доступно только %d filemark'ов", n, found)
}

// BackwardFilemarks пропускает n filemark'ов назад (MTBSFM): позиция —
// сразу после n-го сзади filemark'а, в начале следующей за ним записи.
func (t *Tape) BackwardFilemarks(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n <= 0 {
		return fmt.Errorf("filetape: MTBSFM: count должен быть > 0, получено %d", n)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var markEnds []int64
	off := int64(len(fileMagic))
	for off < t.pos {
		typ, _, next, err := t.recordAt(off)
		if err != nil {
			return err
		}
		if typ == recFilemark {
			markEnds = append(markEnds, next)
		}
		off = next
	}
	if len(markEnds) < n {
		return fmt.Errorf("filetape: MTBSFM(%d): назад доступно только %d filemark'ов", n, len(markEnds))
	}
	t.pos = markEnds[len(markEnds)-n]
	return nil
}

// Rewind перематывает ленту в начало (MTREW).
func (t *Tape) Rewind(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pos = int64(len(fileMagic))
	return nil
}

// EndOfData ставит позицию в конец данных (MTEOM) — позицию дозаписи.
func (t *Tape) EndOfData(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pos = t.size
	return nil
}

// Eject — no-op: у файла-ленты нет механики извлечения.
func (t *Tape) Eject(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Close закрывает файл.
func (t *Tape) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.f.Close()
}

// recordAt читает заголовок записи по смещению off и возвращает тип,
// длину payload и смещение следующей записи.
func (t *Tape) recordAt(off int64) (byte, uint32, int64, error) {
	var hdr [hdrSize]byte
	if _, err := t.f.ReadAt(hdr[:], off); err != nil {
		return 0, 0, 0, fmt.Errorf("filetape: чтение заголовка записи на смещении %d: %w", off, err)
	}
	typ := hdr[0]
	if typ != recData && typ != recFilemark {
		return 0, 0, 0, fmt.Errorf("filetape: неизвестный тип записи 0x%02x на смещении %d", typ, off)
	}
	length := binary.LittleEndian.Uint32(hdr[1:hdrSize])
	next := off + hdrSize + int64(length)
	if next > t.size {
		return 0, 0, 0, fmt.Errorf("filetape: запись на смещении %d выходит за конец данных", off)
	}
	return typ, length, next, nil
}

// appendLocked пишет запись в текущую позицию, усекая хвост файла.
// При заданном лимите ёмкости запись, не влезающая в остаток,
// отклоняется до любых изменений ленты (усечение хвоста перед
// отклонённой записью не выполняется).
func (t *Tape) appendLocked(rec []byte) error {
	base := t.used
	if t.capacity > 0 {
		if t.pos < t.size {
			// Хвост после позиции будет усечён: остаток ёмкости
			// считается по записям до позиции.
			var err error
			base, err = t.usageUpTo(t.pos)
			if err != nil {
				return err
			}
		}
		cost := recordCost(rec)
		if base+cost > t.capacity {
			// Контракт ENOSPC настоящего стримера — типизированной
			// ошибкой: usecase-слои ловят errors.As(*TapeFullError),
			// без разбора текста (конвенция закреплена в domain).
			return errors.Join(&domain.TapeFullError{Capacity: t.capacity}, fmt.Errorf(
				"filetape: no space left on device: лимит %d байт: использовано %d, запись требует ещё %d",
				t.capacity, base, cost))
		}
	}
	if t.pos < t.size {
		if err := t.f.Truncate(t.pos); err != nil {
			return fmt.Errorf("filetape: усечение до смещения %d: %w", t.pos, err)
		}
		t.size = t.pos
	}
	if _, err := t.f.WriteAt(rec, t.pos); err != nil {
		return fmt.Errorf("filetape: запись на смещении %d: %w", t.pos, err)
	}
	t.pos += int64(len(rec))
	t.size += int64(len(rec))
	if t.capacity > 0 {
		t.used = base + recordCost(rec)
	}
	return nil
}

// usageUpTo суммирует полезную ёмкость записей от BOT до смещения off
// (off обязан быть границей записи): блок данных — domain.BlockSize,
// filemark — 1 байт. Вызывается только под мьютексом или до публикации
// ленты из open.
func (t *Tape) usageUpTo(off int64) (int64, error) {
	var used int64
	cur := int64(len(fileMagic))
	for cur < off {
		typ, _, next, err := t.recordAt(cur)
		if err != nil {
			return 0, err
		}
		if typ == recData {
			used += domain.BlockSize
		} else {
			used++
		}
		cur = next
	}
	return used, nil
}

// recordCost — расход полезной ёмкости на запись: блок данных занимает
// domain.BlockSize (хранится добитым до полного блока), filemark — 1 байт.
func recordCost(rec []byte) int64 {
	if rec[0] == recData {
		return domain.BlockSize
	}
	return 1
}

// InvalidTapeFileError — файл существует, но не является файлом-лентой
// (magic не совпадает). Сравнение:
//
//	var e *filetape.InvalidTapeFileError
//	errors.As(err, &e)
type InvalidTapeFileError struct {
	Path string
}

// Error реализует интерфейс error.
func (e *InvalidTapeFileError) Error() string {
	return fmt.Sprintf("файл %q не является лентой filetape", e.Path)
}

// Is поддерживает errors.Is(err, &InvalidTapeFileError{}).
func (e *InvalidTapeFileError) Is(target error) bool {
	_, ok := target.(*InvalidTapeFileError)
	return ok
}
