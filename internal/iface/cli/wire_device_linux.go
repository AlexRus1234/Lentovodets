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

// Открытие ленты с реальным драйвером st: char-устройство → linuxtape,
// обычный файл → filetape (dev/CI на той же машине).

package cli

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"lentovodec/internal/adapter/filetape"
	"lentovodec/internal/adapter/linuxtape"
	"lentovodec/internal/port"
)

// openTapeDevice выбирает реализацию по типу пути: char-устройство
// (группа tape, rootless — SPEC §9.1) — реальный стример, остальное —
// файловая лента. Отсутствующий путь вне /dev создаётся как новая
// файловая лента; под /dev — ошибка probe.
func openTapeDevice(device string) (port.Tape, error) {
	st, statErr := os.Stat(device)
	switch {
	case statErr == nil && st.Mode()&os.ModeCharDevice != 0:
		t, err := linuxtape.Open(device)
		if err != nil {
			return nil, friendlyTapeError(err, device)
		}
		return t, nil
	case statErr == nil:
		t, err := filetape.Open(device)
		if err != nil {
			return nil, friendlyTapeError(err, device)
		}
		return t, nil
	case errors.Is(statErr, fs.ErrNotExist) && !strings.HasPrefix(device, "/dev/"):
		t, err := filetape.Open(device)
		if err != nil {
			return nil, friendlyTapeError(err, device)
		}
		return t, nil
	default:
		return nil, friendlyTapeError(statErr, device)
	}
}
