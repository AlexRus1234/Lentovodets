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

//go:build !tape || !linux || !(amd64 || arm64 || 386 || arm || riscv64 || loong64 || s390x)

// Открытие ленты без реального драйвера: только filetape (dev/CI).
// Сборка с тегом tape на linux подменяет этот файл на wire_device_linux.go.

package cli

import (
	"lentovodec/internal/adapter/filetape"
	"lentovodec/internal/port"
)

// openTapeDevice открывает ленту-файл (dev/CI). Путь под /dev не
// создаётся молча — только честная ошибка probe.
func openTapeDevice(device string) (port.Tape, error) {
	if err := probeDevicePath(device); err != nil {
		return nil, err
	}
	t, err := filetape.Open(device)
	if err != nil {
		return nil, friendlyTapeError(err, device)
	}
	return t, nil
}
