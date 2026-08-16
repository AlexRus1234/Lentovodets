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

//go:build tape

// Package linuxtape реализует port.Tape поверх реального LTO-стримера.
//
// Доступ к устройству — через os.OpenFile("/dev/nst0", ...) и ioctl
// MTIOCTOP (golang.org/x/sys/unix). Пакет компилируется только с build
// tag "tape", потому что на машине разработчика /dev/nst0 нет.
// Реализация (ops.go, linuxtape.go) дополнительно ограничена
// GOOS=linux и архитектурами с asm-generic кодировкой ioctl
// (amd64/arm64/386/arm/riscv64/loong64/s390x); без них пакет пуст.
//
// Команды (docs/FORMAT.md §9, значения — linux/mtio.h): MTWEOF, MTFSF,
// MTBSFM, MTREW, MTEOM, MTOFFL — единый sendTapeCommand(fd, op, count).
//
// Тесты — только в test/hardware/ под тем же тегом, запускаются вручную.
package linuxtape
