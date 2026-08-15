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
	mu   sync.Mutex
	f    *os.File
	pos  int64 // смещение следующей читаемой/пишемой записи
	size int64 // логический размер данных (>= len(fileMagic))
}

// Open открывает (или создаёт) файл-ленту по пути path. Существующий
// непустой файл должен быть файлом-лентой, иначе InvalidTapeFileError.
func Open(path string) (*Tape, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("filetape: открытие %q: %w", path, err)
	}
	t := &Tape{f: f}
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
func (t *Tape) appendLocked(rec []byte) error {
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
	return nil
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
