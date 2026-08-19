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

// Ручные тесты adapter/linuxtape на реальном стримере.
//
// Запуск (машина с приводом, пользователь в группе tape):
//
//	LENTOVODEC_TAPE_DEVICE=/dev/nst0 go test -tags=tape ./test/hardware/ -v -count=1
//
// ВНИМАНИЕ: тесты разрушают все данные на кассете.

package hardware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"lentovodec/internal/adapter/linuxtape"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// devicePath возвращает путь к устройству из env или /dev/nst0;
// пропускает тест, если устройства нет.
func devicePath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("LENTOVODEC_TAPE_DEVICE")
	if path == "" {
		path = "/dev/nst0"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("нет устройства %s: %v", path, err)
	}
	return path
}

// openTape открывает устройство и закрывает его по завершении теста.
func openTape(t *testing.T) port.Tape {
	t.Helper()
	tp, err := linuxtape.Open(devicePath(t))
	if err != nil {
		t.Fatalf("linuxtape.Open: %v", err)
	}
	t.Cleanup(func() {
		tp.Close() //nolint:errcheck // очистка
	})
	return tp
}

// TestTape_LabelWriteRewindRead — раскладка FORMAT §4: ярлык + двойной
// EOF (EOD), затем перечитать с начала. Запись идёт от BOT: запись в
// текущей позиции усекает хвост — раскладка детерминирована независимо
// от того, что было на кассете.
func TestTape_LabelWriteRewindRead(t *testing.T) {
	tp := openTape(t)
	ctx := context.Background()

	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind (подготовка): %v", err)
	}
	label := bytes.Repeat([]byte{0xAB}, 512)
	label = append(label, make([]byte, domain.BlockSize-512)...)
	if err := tp.WriteBlock(ctx, label); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if err := tp.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	if err := tp.WriteEOF(ctx); err != nil { // второй EOF = EOD
		t.Fatalf("WriteEOF(EOD): %v", err)
	}

	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	got, err := tp.ReadBlock(ctx)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if !bytes.Equal(got, label) {
		t.Fatalf("блок после перемотки не совпал: len=%d", len(got))
	}
	// Далее: метка после ярлыка, метка EOD.
	for i := 0; i < 2; i++ {
		if _, err := tp.ReadBlock(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("чтение %d-й метки: %v, хочу io.EOF", i+1, err)
		}
	}
}

// TestTape_SessionNavigation — три «сессии» (блок+метка ×3), навигация
// MTFSF/MTBSFM/MTEOM по правилам FORMAT §9. Запись от BOT усекает
// хвост: раскладка [s1][FM1][s2][FM2][s3][FM3], позиция после записи —
// EOD (за FM3).
//
// MTBSFM(n) из EOD — позиция после n-й метки позади, начало следующего
// за ней файла (канон ROADMAP Этап 4; та же семантика у FakeTape и
// filetape): BSFM(1) — no-op (мы уже за FM3), BSFM(2) — начало s3,
// BSFM(3) — начало s2. К s1 BSFM не приводит принципиально: перед ним
// меток нет — только Rewind.
func TestTape_SessionNavigation(t *testing.T) {
	tp := openTape(t)
	ctx := context.Background()

	s1 := bytes.Repeat([]byte{0x11}, domain.BlockSize)
	s2 := bytes.Repeat([]byte{0x22}, domain.BlockSize)
	s3 := bytes.Repeat([]byte{0x33}, domain.BlockSize)
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind (подготовка): %v", err)
	}
	for i, block := range [][]byte{s1, s2, s3} {
		if err := tp.WriteBlock(ctx, block); err != nil {
			t.Fatalf("WriteBlock(%d): %v", i+1, err)
		}
		if err := tp.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF(%d): %v", i+1, err)
		}
	}

	toEOD := func() {
		t.Helper()
		if err := tp.Rewind(ctx); err != nil {
			t.Fatalf("Rewind: %v", err)
		}
		if err := tp.ForwardFilemarks(ctx, 3); err != nil {
			t.Fatalf("ForwardFilemarks(3) в EOD: %v", err)
		}
	}

	// BSFM(1) из EOD — no-op: чтение даёт EOF (за FM3 данных нет).
	toEOD()
	if err := tp.BackwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("BackwardFilemarks(1) из EOD: %v", err)
	}
	if _, err := tp.ReadBlock(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("чтение после BSFM(1): %v, хочу io.EOF (no-op)", err)
	}

	// BSFM(2) из EOD — начало последнего файла s3.
	toEOD()
	if err := tp.BackwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("BackwardFilemarks(2): %v", err)
	}
	got, err := tp.ReadBlock(ctx)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if !bytes.Equal(got, s3) {
		t.Fatal("MTBSFM(2) привёл не к последней сессии s3")
	}

	// BSFM(3) из EOD — начало s2.
	toEOD()
	if err := tp.BackwardFilemarks(ctx, 3); err != nil {
		t.Fatalf("BackwardFilemarks(3): %v", err)
	}
	got, err = tp.ReadBlock(ctx)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if !bytes.Equal(got, s2) {
		t.Fatal("MTBSFM(3) привёл не к сессии s2")
	}

	// MTEOM из середины: позиция дозаписи; новый блок и метка должны
	// читаться как четвёртая сессия ([s1][FM1][s2][FM2][s3][FM3][s4][FM4],
	// чтение s4 — FSF(3): за FM3, в начале s4).
	if err := tp.EndOfData(ctx); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	s4 := bytes.Repeat([]byte{0x44}, domain.BlockSize)
	if err := tp.WriteBlock(ctx, s4); err != nil {
		t.Fatalf("WriteBlock(append): %v", err)
	}
	if err := tp.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF(append): %v", err)
	}
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if err := tp.ForwardFilemarks(ctx, 3); err != nil {
		t.Fatalf("ForwardFilemarks(3): %v", err)
	}
	got, err = tp.ReadBlock(ctx)
	if err != nil {
		t.Fatalf("ReadBlock(дозапись): %v", err)
	}
	if !bytes.Equal(got, s4) {
		t.Fatal("дозапись в EOD не читается четвёртой сессией")
	}
}

// TestTape_Eject — MTOFFL извлекает кассету. Запускать только если
// кассету можно вставить обратно вручную.
func TestTape_Eject(t *testing.T) {
	tp := openTape(t)
	if err := tp.Eject(context.Background()); err != nil {
		t.Fatalf("Eject: %v", err)
	}
}
