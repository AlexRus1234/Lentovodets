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
