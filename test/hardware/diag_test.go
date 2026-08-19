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

//go:build tape && linux

// Диагностика MTBSFM на реальном стримере. Повод: первый аппаратный
// прогон (IBM ULT3580-HH5, LTO-5) — MTBSFM(2) сразу после чтения блока
// завершается EIO. Тест изолирует переменные (count>1 против поштучных,
// до чтения против после чтения) и логирует позицию MTIOCGET до/после
// каждого шага — сопоставлять с sudo dmesg (sense-данные).
//
// Запуск (включается явно, кассету ПЕРЕЗАПИСЫВАЕТ от BOT):
//
//	LENTOVODEC_TAPE_DIAG=1 LENTOVODEC_TAPE_DEVICE=/dev/nst0 \
//		go test -tags=tape ./test/hardware/ -v -count=1 -run TestTapeDiag
//
// Тест намеренно не падает на EIO: это диагност, каждая ошибка —
// строчка в отчёте. Ожидания по канону (FORMAT §9, ROADMAP Этап 4:
// MTBSFM(n) — позиция после n-й метки позади; из EOD BSFM(1) — no-op,
// BSFM(2) — начало последнего файла).

package hardware

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"

	"lentovodec/internal/domain"
)

// Коды MTIOCTOP/MTIOCGET — из include/uapi/linux/mtio.h, кодировка
// asm-generic (те же значения, что internal/adapter/linuxtape/ops.go;
// здесь свои константы — диагностика самодостаточна и не лезет в
// неэкспортируемое адаптера).
const (
	diagIOCTOP = 0x40086d01
	diagIOCGET = 0x80306d02
	diagWEOF   = 5
	diagFSF    = 1
	diagBSFM   = 10
	diagREW    = 6
)

// mtopDiag — struct mtop { short mt_op; int mt_count; }.
type mtopDiag struct {
	op    int16
	count int32
}

// mtgetDiag — struct mtget: 5 long + 2 int = 48 байт на 64-бит.
type mtgetDiag struct {
	mtType   int64
	mtResid  int64
	mtDsreg  int64
	mtGstat  int64
	mtErreg  int64
	mtFileno int32
	mtBlkno  int32
}

// diagCmd выполняет MTIOCTOP (повтор при EINTR — как sendTapeCommand).
func diagCmd(fd uintptr, op int16, count int32) error {
	arg := mtopDiag{op: op, count: count}
	for {
		_, _, errno := unix.Syscall(
			unix.SYS_IOCTL, fd, diagIOCTOP, uintptr(unsafe.Pointer(&arg)))
		if errno == 0 {
			return nil
		}
		if errno == unix.EINTR {
			continue
		}
		return errno
	}
}

// diagPos — позиция драйвера по MTIOCGET (аналог mt status).
func diagPos(f *os.File) string {
	var g mtgetDiag
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL, f.Fd(), diagIOCGET, uintptr(unsafe.Pointer(&g)))
	if errno != 0 {
		return fmt.Sprintf("MTIOCGET: %v", errno)
	}
	return fmt.Sprintf("fileno=%d blkno=%d resid=%d", g.mtFileno, g.mtBlkno, g.mtResid)
}

// diagStep — одна mt-команда с логом позиции до/после.
func diagStep(t *testing.T, f *os.File, name string, op int16, count int32) {
	t.Helper()
	before := diagPos(f)
	err := diagCmd(f.Fd(), op, count)
	after := diagPos(f)
	if err != nil {
		t.Logf("[%s] op=%d count=%d: %v (%s → %s; см. sudo dmesg)",
			name, op, count, err, before, after)
		return
	}
	t.Logf("[%s] op=%d count=%d: ok (%s → %s)", name, op, count, before, after)
}

// diagWrite пишет один блок-шаблон (domain.BlockSize байт; в
// variable-block режиме стримера — один блок).
func diagWrite(t *testing.T, f *os.File, name string, pattern byte) {
	t.Helper()
	block := bytes.Repeat([]byte{pattern}, domain.BlockSize)
	if _, err := f.Write(block); err != nil {
		t.Fatalf("[%s] запись блока 0x%02X: %v", name, pattern, err)
	}
}

// diagRead читает один блок с логом результата: шаблон / EOF (метка
// или EOD) / ошибка.
func diagRead(t *testing.T, f *os.File, name string) {
	t.Helper()
	buf := make([]byte, domain.BlockSize)
	n, err := io.ReadFull(f, buf)
	switch {
	case err == nil || (err == io.ErrUnexpectedEOF && n > 0):
		t.Logf("[%s] чтение: %d байт, шаблон 0x%02X (позиция %s)",
			name, n, buf[0], diagPos(f))
	case err == io.EOF:
		t.Logf("[%s] чтение: EOF — метка/EOD (позиция %s)", name, diagPos(f))
	default:
		t.Logf("[%s] чтение: %v (позиция %s; см. sudo dmesg)", name, err, diagPos(f))
	}
}

// TestTapeDiag_BSFM — сценарии MTBSFM на раскладке
// [b1 0x11][FM1][b2 0x22][FM2][b3 0x33][FM3], EOD — после FM3.
//
// С1  BSFM(1) из EOD — канон: no-op (чтение даёт EOF).
// С1b BSFM(1) дважды — эквивалент BSFM(2): начало b3.
// С2  BSFM(2) из EOD без предшествующего чтения — канон: начало b3.
// С3  BSFM(3) из EOD — канон: начало b2.
// С4  чтение b3, затем BSFM(1) поштучно — чтение/Read-Ahead + одиночный
//
//	шаг назад (гипотеза: после чтения падает только count>1).
//
// С5  точный сценарий упавшего SessionNavigation: чтение b2, затем
//
//	BSFM(2) разом.
//
// С6  BSFM(99) — за пределы меток (ожидаем ошибку — какой код?).
func TestTapeDiag_BSFM(t *testing.T) {
	if os.Getenv("LENTOVODEC_TAPE_DIAG") == "" {
		t.Skip("диагностика выключена: LENTOVODEC_TAPE_DIAG=1")
	}
	f, err := os.OpenFile(devicePath(t), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("открытие устройства: %v", err)
	}
	defer func() { _ = f.Close() }()
	if got := unsafe.Sizeof(mtgetDiag{}); got != 48 {
		t.Logf("ВНИМАНИЕ: sizeof(mtget)=%d, ожидалось 48 — поля MTIOCGET будут битые", got)
	}

	// Подготовка: запись от BOT усекает хвост — раскладка детерминирована.
	if err := diagCmd(f.Fd(), diagREW, 1); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	for i, pattern := range []byte{0x11, 0x22, 0x33} {
		diagWrite(t, f, "prep", pattern)
		if err := diagCmd(f.Fd(), diagWEOF, 1); err != nil {
			t.Fatalf("weof(%d): %v", i+1, err)
		}
	}
	t.Logf("prep: [b1][FM1][b2][FM2][b3][FM3], позиция %s", diagPos(f))

	toEOD := func() {
		t.Helper()
		if err := diagCmd(f.Fd(), diagREW, 1); err != nil {
			t.Fatalf("rewind: %v", err)
		}
		if err := diagCmd(f.Fd(), diagFSF, 3); err != nil {
			t.Fatalf("fsf(3) в EOD: %v", err)
		}
	}

	// С1: BSFM(1) из EOD — no-op; чтение должно дать EOF.
	diagStep(t, f, "C1 BSFM(1) из EOD", diagBSFM, 1)
	diagRead(t, f, "C1")

	// С1b: два одиночных BSFM(1) — эквивалент BSFM(2).
	toEOD()
	diagStep(t, f, "C1b BSFM(1) #1", diagBSFM, 1)
	diagStep(t, f, "C1b BSFM(1) #2", diagBSFM, 1)
	diagRead(t, f, "C1b (ожидание b3=0x33)")

	// С2: BSFM(2) из EOD без чтения — начало b3.
	toEOD()
	diagStep(t, f, "C2 BSFM(2) из EOD", diagBSFM, 2)
	diagRead(t, f, "C2 (ожидание b3=0x33)")

	// С3: BSFM(3) из EOD — начало b2.
	toEOD()
	diagStep(t, f, "C3 BSFM(3) из EOD", diagBSFM, 3)
	diagRead(t, f, "C3 (ожидание b2=0x22)")

	// С4: позиционирование FSF на b3, ЧТЕНИЕ (read-ahead), поштучные шаги.
	if err := diagCmd(f.Fd(), diagREW, 1); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if err := diagCmd(f.Fd(), diagFSF, 2); err != nil {
		t.Fatalf("fsf(2): %v", err)
	}
	t.Logf("[C4] после FSF(2) позиция %s", diagPos(f))
	diagRead(t, f, "C4 чтение b3")
	diagStep(t, f, "C4 BSFM(1) после чтения", diagBSFM, 1)
	diagStep(t, f, "C4 BSFM(1) ещё раз", diagBSFM, 1)
	diagRead(t, f, "C4 (ожидание b3=0x33)")

	// С5: сценарий упавшего теста — чтение b2, затем BSFM(2) разом.
	if err := diagCmd(f.Fd(), diagREW, 1); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if err := diagCmd(f.Fd(), diagFSF, 1); err != nil {
		t.Fatalf("fsf(1): %v", err)
	}
	diagRead(t, f, "C5 чтение b2")
	diagStep(t, f, "C5 BSFM(2) после чтения", diagBSFM, 2)
	diagRead(t, f, "C5 (ожидание b3=0x33)")

	// С6: BSFM(99) — за пределы доступных меток.
	toEOD()
	diagStep(t, f, "C6 BSFM(99) из EOD", diagBSFM, 99)

	// Финал: BOT — кассета в предсказуемом состоянии.
	if err := diagCmd(f.Fd(), diagREW, 1); err != nil {
		t.Logf("финальный rewind: %v", err)
	}
	t.Logf("итог: позиция %s; при EIO в сценариях — sudo dmesg | tail -40", diagPos(f))
}
