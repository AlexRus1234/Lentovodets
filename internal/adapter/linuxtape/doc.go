//go:build tape

// Package linuxtape реализует port.Tape поверх реального LTO-стримера.
//
// Доступ к устройству — через os.OpenFile("/dev/nst0", ...) и ioctl
// (golang.org/x/sys/unix). Пакет компилируется только с build tag "tape",
// потому что на машине разработчика /dev/nst0 нет.
//
// Команды (docs/FORMAT.md §9): MTWEOF, MTFSF, MTBSFM, MTREW, MTEOM, MTOFFL
// — единый sendTapeCommand(fd, op, count).
//
// Тесты — только в test/hardware/ под тем же тегом, запускаются вручную.
package linuxtape
