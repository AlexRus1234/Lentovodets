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
// EOF (EOD), затем перечитать с начала.
func TestTape_LabelWriteRewindRead(t *testing.T) {
	tp := openTape(t)
	ctx := context.Background()

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

// TestTape_SessionNavigation — две «сессии» (блок+метка ×2), навигация
// MTFSF/MTBSFM/MTEOM по правилам FORMAT §9.
func TestTape_SessionNavigation(t *testing.T) {
	tp := openTape(t)
	ctx := context.Background()

	s1 := bytes.Repeat([]byte{0x11}, domain.BlockSize)
	s2 := bytes.Repeat([]byte{0x22}, domain.BlockSize)
	for i, block := range [][]byte{s1, s2} {
		if err := tp.WriteBlock(ctx, block); err != nil {
			t.Fatalf("WriteBlock(%d): %v", i+1, err)
		}
		if err := tp.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF(%d): %v", i+1, err)
		}
	}

	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	// MTFSF(1) — начало сессии 2.
	if err := tp.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("ForwardFilemarks(1): %v", err)
	}
	got, err := tp.ReadBlock(ctx)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if !bytes.Equal(got, s2) {
		t.Fatal("MTFSF(1) привёл не ко второй сессии")
	}
	// MTBSFM(2) от позиции за меткой сессии 2 — назад к началу сессии 1.
	if err := tp.BackwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("BackwardFilemarks(2): %v", err)
	}
	got, err = tp.ReadBlock(ctx)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if !bytes.Equal(got, s1) {
		t.Fatal("MTBSFM(2) привёл не к первой сессии")
	}
	// MTEOM: позиция дозаписи; новый блок и метка должны читаться в хвосте.
	if err := tp.EndOfData(ctx); err != nil {
		t.Fatalf("EndOfData: %v", err)
	}
	s3 := bytes.Repeat([]byte{0x33}, domain.BlockSize)
	if err := tp.WriteBlock(ctx, s3); err != nil {
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
	if !bytes.Equal(got, s3) {
		t.Fatal("дозапись в EOD не читается третьей сессией")
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
