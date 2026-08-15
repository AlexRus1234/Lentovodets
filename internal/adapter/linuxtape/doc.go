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
