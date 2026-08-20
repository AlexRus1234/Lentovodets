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

// Порт ленточного накопителя: блоки, filemark'и, перемотки.
// См. docs/FORMAT.md §1–2 и §9, docs/ARCHITECTURE.md §3.

package port

import (
	"context"

	"lentovodec/internal/domain"
)

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

// TapeDiagnostics — необязательная диагностика привода. Реализации Tape
// могут не поддерживать этот интерфейс (например, filetape).
type TapeDiagnostics interface {
	TapeAlerts(ctx context.Context) ([]domain.TapeAlert, error)
}
