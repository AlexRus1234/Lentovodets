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
	"strings"
	"testing"

	"lentovodec/internal/port"
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
