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

// Константы и низкоуровневый ioctl-примитив драйвера st (SCSI tape).
// Значения — из include/uapi/linux/mtio.h ядра Linux.

package linuxtape

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mtIOCTOP — код ioctl MTIOCTOP = _IOW('m', 1, struct mtop) при
// кодировке asm-generic (8-байтовый mtop: _IOC_NRBITS=8, SIZEBITS=14,
// DIRBITS=2, _IOC_WRITE=1). Именно так на x86/arm/riscv/loong64/s390x.
// На MIPS и PowerPC кодировка другая — эти архитектуры исключены
// build-тегом файлов пакета.
const mtIOCTOP = 0x40086d01

// Коды операций MTIOCTOP (linux/mtio.h).
const (
	mtWEOF = 5  // MTWEOF: write an end-of-file record (mark)
	mtFSF  = 1  // MTFSF: forward space over FileMark, position at first record of next file
	mtBSFM = 10 // MTBSFM: backward space FileMark, position at FM (после него)
	mtREW  = 6  // MTREW: rewind
	mtEOM  = 12 // MTEOM: goto end of recorded media, ready for appending
	mtOFFL = 7  // MTOFFL: rewind and put the drive offline (eject)
)

// mtop — аргумент MTIOCTOP: struct mtop { short mt_op; int mt_count; }
// из linux/mtio.h. Размер 8 байт: int16 + 2 байта выравнивания + int32,
// что совпадает с C-раскладкой на всех поддерживаемых архитектурах.
type mtop struct {
	op    int16
	count int32
}

// sendTapeCommand выполняет MTIOCTOP с операцией op и счётчиком count
// на файловом дескрипторе fd устройства /dev/nst*.
func sendTapeCommand(fd uintptr, op int16, count int32) error {
	arg := mtop{op: op, count: count}
	for {
		_, _, errno := unix.Syscall(
			unix.SYS_IOCTL,
			fd,
			uintptr(mtIOCTOP),
			uintptr(unsafe.Pointer(&arg)),
		)
		if errno == 0 {
			return nil
		}
		if errno == unix.EINTR {
			// Ожидание в st_ioctl прерываемо до старта команды (сигнал,
			// в т.ч. SIGURG от вытеснения горутин Go); ioctl не
			// перезапускается ядром даже с SA_RESTART — повтор
			// безопасен: команда ещё не началась.
			continue
		}
		return fmt.Errorf("linuxtape: ioctl op=%d count=%d: %w", op, count, errno)
	}
}
