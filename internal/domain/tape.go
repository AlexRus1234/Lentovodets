// Константы двоичного формата и ярлык кассеты.
// См. docs/FORMAT.md §2 и §5.

package domain

// Константы двоичного формата ленты (канон — docs/FORMAT.md §2).
const (
	// Magic — строковая сигнатура в ярлыке ленты. Кассеты legacy
	// nil-backup (magic "NIL_BACKUP_TAPE") намеренно не читаются.
	Magic = "LENTOVODEC_TAPE_V2"

	// FormatVersion — текущая версия формата; инкрементируется при
	// любом изменении формата.
	FormatVersion = 2

	// BlockSize — размер блока на ленте (256 KiB; типично для LTO-5..9).
	BlockSize = 256 * 1024

	// CopyBuffer — буфер копирования файлов в tar (4 MiB).
	CopyBuffer = 4 * 1024 * 1024
)

// TapeLabel — JSON-ярлык кассеты в первом блоке ленты; JSON добивается
// нулями до BlockSize. Канон полей — docs/FORMAT.md §5.
type TapeLabel struct {
	Magic         string `json:"magic"`          // всегда Magic
	FormatVersion int    `json:"format_version"` // версия формата
	Name          string `json:"name"`           // уникальное имя кассеты в каталоге
	UUID          string `json:"uuid"`           // RFC-4122 v4, канонический lowercase
	FormattedAt   string `json:"formatted_at"`   // RFC-3339, UTC
}

// TapeInfo — снимок состояния ленты для команды `tape info`.
type TapeInfo struct {
	Label    TapeLabel
	Filemark int // сколько filemark'ов от начала ленты; -1 — неизвестно
}
