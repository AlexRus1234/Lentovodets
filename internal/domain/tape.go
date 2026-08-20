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

// Константы двоичного формата и ярлык кассеты.
// См. docs/FORMAT.md §2 и §5.

package domain

import (
	"fmt"
	"time"
)

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

// ParseFormattedAt разбирает FormattedAt (RFC-3339). Единая точка
// валидации поля: DecodeLabel проверяет ярлык при чтении с ленты,
// RegisterTape'ам нужен Unix-секунды.
func (l TapeLabel) ParseFormattedAt() (time.Time, error) {
	ts, err := time.Parse(time.RFC3339, l.FormattedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("ярлык %s: некорректный formatted_at %q: %w",
			l.UUID, l.FormattedAt, err)
	}
	return ts, nil
}

// TapeInfo — снимок состояния ленты для команды `tape info`.
type TapeInfo struct {
	Label    TapeLabel
	Filemark int // сколько filemark'ов от начала ленты; -1 — неизвестно
	Alerts   []TapeAlert
}

// TapeAlert — активный флаг диагностики привода по стандарту TapeAlert.
type TapeAlert struct {
	Name     string
	Code     int
	Critical bool
}
