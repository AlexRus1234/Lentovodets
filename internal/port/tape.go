// Порт ленточного накопителя: блоки, filemark'и, перемотки.
// См. docs/FORMAT.md §1–2 и §9, docs/ARCHITECTURE.md §3.

package port

import "context"

// Tape — последовательный блочный доступ к стримерной ленте.
//
// Лента — последовательность блоков размером domain.BlockSize,
// разделённых filemark'ами (EOF-маркерами, MTWEOF). Логические позиции
// между filemark'ами нумеруются с 1; правила перемотки — docs/FORMAT.md §9.
//
// Реализации: adapter/linuxtape (/dev/nst*, build tag tape),
// adapter/filetape (лента-как-файл, dev/CI).
type Tape interface {
	// ReadBlock читает один блок длиной до domain.BlockSize.
	// Возвращает io.EOF при достижении filemark'а или EOD.
	ReadBlock(ctx context.Context) ([]byte, error)

	// WriteBlock записывает один блок. Блок короче domain.BlockSize
	// добивается нулями; блок длиннее — ошибка.
	WriteBlock(ctx context.Context, block []byte) error

	// WriteEOF записывает filemark (MTWEOF).
	WriteEOF(ctx context.Context) error

	// ForwardFilemarks пропускает n filemark'ов вперёд (MTFSF) и
	// останавливается в начале следующей за ними записи.
	ForwardFilemarks(ctx context.Context, n int) error

	// BackwardFilemarks пропускает n filemark'ов назад (MTBSFM).
	BackwardFilemarks(ctx context.Context, n int) error

	// Rewind перематывает ленту в начало (MTREW).
	Rewind(ctx context.Context) error

	// EndOfData перемещает ленту в конец данных (MTEOM) — позицию
	// дозаписи новой сессии.
	EndOfData(ctx context.Context) error

	// Eject извлекает кассету (MTOFFL).
	Eject(ctx context.Context) error

	// Close освобождает дескриптор устройства.
	Close() error
}
