// FakeTape — детерминированный двойник port.Tape для тестов формата.
// См. docs/TESTING.md §3.1.

package testutil

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// tapeItem — один элемент ленты: блок данных или filemark.
type tapeItem struct {
	data   []byte // содержимое блока; nil для filemark
	isMark bool
}

// FakeTape хранит ленту как последовательность блоков и filemark'ов.
//
// Семантика повторяет драйвер st (docs/FORMAT.md §9):
//
//   - ReadBlock возвращает очередной блок; на filemark'е — io.EOF,
//     потребляя его (следующее чтение начнёт следующий файл);
//   - запись идёт в текущую позицию и уничтожает всё после неё
//     (как перезапись хвоста реальной ленты);
//   - EndOfData ставит позицию за последним filemark'ом.
type FakeTape struct {
	mu      sync.Mutex
	items   []tapeItem
	pos     int  // индекс следующего элемента
	ejected bool // был ли вызван Eject
}

// NewFakeTape создаёт пустую ленту.
func NewFakeTape() *FakeTape {
	return &FakeTape{}
}

// ReadBlock читает один блок. На filemark'е и в конце данных — io.EOF.
func (t *FakeTape) ReadBlock(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pos >= len(t.items) {
		return nil, io.EOF
	}
	item := t.items[t.pos]
	t.pos++
	if item.isMark {
		return nil, io.EOF
	}
	out := make([]byte, len(item.data))
	copy(out, item.data)
	return out, nil
}

// WriteBlock записывает блок в текущую позицию, затирая хвост ленты.
func (t *FakeTape) WriteBlock(ctx context.Context, block []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	item := tapeItem{data: make([]byte, len(block))}
	copy(item.data, block)
	t.truncateLocked()
	t.items = append(t.items, item)
	t.pos = len(t.items)
	return nil
}

// WriteEOF ставит filemark в текущую позицию, затирая хвост ленты.
func (t *FakeTape) WriteEOF(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.truncateLocked()
	t.items = append(t.items, tapeItem{isMark: true})
	t.pos = len(t.items)
	return nil
}

// ForwardFilemarks пропускает n filemark'ов вперёд (MTFSF): позиция —
// сразу после n-го filemark'а, т.е. в начале следующего файла.
func (t *FakeTape) ForwardFilemarks(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	skipped := 0
	for i := t.pos; i < len(t.items); i++ {
		if !t.items[i].isMark {
			continue
		}
		skipped++
		if skipped == n {
			t.pos = i + 1
			return nil
		}
	}
	return fmt.Errorf("faketape: MTFSF(%d): вперёд доступно только %d filemark'ов", n, skipped)
}

// BackwardFilemarks пропускает n filemark'ов назад (MTBSFM): позиция —
// сразу после n-го filemark'а, т.е. в начале файла, следующего за ним.
func (t *FakeTape) BackwardFilemarks(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	found := 0
	for i := t.pos - 1; i >= 0; i-- {
		if !t.items[i].isMark {
			continue
		}
		found++
		if found == n {
			t.pos = i + 1
			return nil
		}
	}
	return fmt.Errorf("faketape: MTBSFM(%d): назад доступно только %d filemark'ов", n, found)
}

// Rewind перематывает ленту в начало.
func (t *FakeTape) Rewind(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pos = 0
	return nil
}

// EndOfData ставит позицию в конец данных — за последний filemark.
func (t *FakeTape) EndOfData(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pos = len(t.items)
	return nil
}

// Eject помечает ленту извлечённой.
func (t *FakeTape) Eject(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ejected = true
	return nil
}

// Close — no-op, нужен для port.Tape.
func (t *FakeTape) Close() error { return nil }

// BlockCount возвращает число блоков данных на ленте.
func (t *FakeTape) BlockCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, item := range t.items {
		if !item.isMark {
			n++
		}
	}
	return n
}

// MarkCount возвращает число filemark'ов на ленте.
func (t *FakeTape) MarkCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, item := range t.items {
		if item.isMark {
			n++
		}
	}
	return n
}

// Ejected сообщает, был ли вызван Eject.
func (t *FakeTape) Ejected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ejected
}

// Snapshot возвращает копию ленты: блоки по порядку и позиции filemark'ов.
// Позиция filemark'а — это число блоков, записанных до него
// (filemark после 1-го блока → 1); после записи сессии по docs/FORMAT.md §4
// метки равны [1, 2, 2+len(tar)].
func (t *FakeTape) Snapshot() (blocks [][]byte, marks []int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	blocks = make([][]byte, 0, len(t.items))
	marks = make([]int, 0, len(t.items))
	for _, item := range t.items {
		if item.isMark {
			marks = append(marks, len(blocks))
			continue
		}
		b := make([]byte, len(item.data))
		copy(b, item.data)
		blocks = append(blocks, b)
	}
	return blocks, marks
}

// truncateLocked отбрасывает элементы после текущей позиции.
func (t *FakeTape) truncateLocked() {
	if t.pos < len(t.items) {
		t.items = t.items[:t.pos]
	}
}
