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

package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"lentovodec/internal/domain"
)

func TestTypedErrors(t *testing.T) {
	alien := errors.New("чужая ошибка")

	tests := []struct {
		name    string
		err     error
		msgPart string
		target  error
	}{
		{
			"лента заполнена без ёмкости",
			&domain.TapeFullError{Written: 12345},
			"лента заполнена: записано 12345 байт",
			&domain.TapeFullError{},
		},
		{
			"лента заполнена с ёмкостью",
			&domain.TapeFullError{Written: 100, Capacity: 200},
			"из ~200",
			&domain.TapeFullError{},
		},
		{
			"несовпадение ярлыка",
			&domain.LabelMismatchError{Expected: "uuid-1", Actual: "uuid-2"},
			`"uuid-2"`,
			&domain.LabelMismatchError{},
		},
		{
			"чужой формат",
			&domain.ForeignFormatError{Magic: "NIL_BACKUP_TAPE"},
			"NIL_BACKUP_TAPE",
			&domain.ForeignFormatError{},
		},
		{
			"пустая лента",
			&domain.BlankTapeError{},
			"лента пуста",
			&domain.BlankTapeError{},
		},
		{
			"версия новее",
			&domain.NewerFormatError{Found: 3, Supported: 2},
			"3 новее поддерживаемой 2",
			&domain.NewerFormatError{},
		},
		{
			"кассета не найдена",
			&domain.TapeNotFoundError{UUID: "u-7"},
			"кассета u-7 не найдена",
			&domain.TapeNotFoundError{},
		},
		{
			"сессия не найдена",
			&domain.SessionNotFoundError{SessionID: 42},
			"сессия 42 не найдена",
			&domain.SessionNotFoundError{},
		},
		{
			"нет здоровой копии",
			&domain.NoHealthyCopyError{Path: "/tank/data/movie.mkv"},
			"/tank/data/movie.mkv",
			&domain.NoHealthyCopyError{},
		},
		{
			"уже отформатирована",
			&domain.AlreadyFormattedError{Name: "media-001", UUID: "u-1"},
			"media-001",
			&domain.AlreadyFormattedError{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := tt.err.Error(); !strings.Contains(msg, tt.msgPart) {
				t.Errorf("Error() = %q, ожидается вхождение %q", msg, tt.msgPart)
			}
			if !errors.Is(tt.err, tt.target) {
				t.Errorf("errors.Is(%v, %T) = false, want true", tt.err, tt.target)
			}
			wrapped := fmt.Errorf("обёрнуто: %w", tt.err)
			if !errors.Is(wrapped, tt.target) {
				t.Errorf("errors.Is(wrapped, %T) = false, want true", tt.target)
			}
			if errors.Is(tt.err, alien) {
				t.Errorf("errors.Is(%v, чужой тип) = true, want false", tt.err)
			}
		})
	}
}

func TestErrorsAsExtractsDetails(t *testing.T) {
	wrapped := fmt.Errorf("запись прервана: %w",
		&domain.TapeFullError{Written: 512, Capacity: 1024})

	var tf *domain.TapeFullError
	if !errors.As(wrapped, &tf) {
		t.Fatal("errors.As не извлёк TapeFullError")
	}
	if tf.Written != 512 || tf.Capacity != 1024 {
		t.Errorf("TapeFullError = %+v, want {Written:512 Capacity:1024}", tf)
	}

	var nf *domain.SessionNotFoundError
	if !errors.As(fmt.Errorf("w: %w", &domain.SessionNotFoundError{SessionID: 9}), &nf) {
		t.Fatal("errors.As не извлёк SessionNotFoundError")
	}
	if nf.SessionID != 9 {
		t.Errorf("SessionID = %d, want 9", nf.SessionID)
	}

	var no *domain.SessionNotFoundError
	if errors.As(wrapped, &no) {
		t.Error("errors.As не должен находить SessionNotFoundError в TapeFullError")
	}
}
