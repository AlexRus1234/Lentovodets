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

package tapeformat

import (
	"context"
	"strings"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// encodeBlocks обязан возвращать ошибку для значений, которые не умеет
// кодировать encoding/json (наши wire-типы к таким не относятся, но ветка
// обязана работать).
func TestEncodeBlocks_UnmarshalableValue(t *testing.T) {
	_, err := encodeBlocks(struct{ Ch chan int }{}, 0)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "tapeformat: JSON") {
		t.Errorf("err = %v, want вхождение %q", err, "tapeformat: JSON")
	}
}

// discardProgress — заглушка для nil-прогресса; методы не должны паниковать.
func TestDiscardProgress(t *testing.T) {
	var prog port.ProgressReporter = discardProgress{}
	prog.Update(port.ProgressUpdate{Phase: port.PhaseWrite, CurrentFile: "/x"})
	prog.Done()
	prog.Fail(nil)
}

// readIndex: индекс старой ленты без part/continues → Part=1, Continues="";
// явные значения сохраняются. Нормализация <1 → 1 защищает и от
// отрицательного мусора.
func TestReadIndex_PartNormalization(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		part      int32
		continues string
		wantPart  int32
	}{
		{"старая лента (полей нет)", 0, "", 1},
		{"отрицательный мусор", -3, "", 1},
		{"часть цепочки", 3, "uuid-prev", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tape := testutil.NewFakeTape()
			idx := SessionIndex{
				FormatVersion: domain.FormatVersion,
				SessionNum:    1,
				Type:          domain.SessionFull,
				JobRunID:      "run",
				Timestamp:     1700000000,
				JobName:       "j",
				Part:          tc.part,
				Continues:     tc.continues,
			}
			if err := WriteSession(ctx, tape, idx, testutil.NewMapFS(nil), nil); err != nil {
				t.Fatalf("WriteSession: %v", err)
			}
			if err := tape.Rewind(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := readIndex(ctx, tape)
			if err != nil {
				t.Fatalf("readIndex: %v", err)
			}
			if got.Part != tc.wantPart {
				t.Errorf("Part = %d; want %d", got.Part, tc.wantPart)
			}
			if got.Continues != tc.continues {
				t.Errorf("Continues = %q; want %q", got.Continues, tc.continues)
			}
		})
	}
}
