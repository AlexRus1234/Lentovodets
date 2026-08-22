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
		{
			"файл больше кассеты",
			&domain.FileTooLargeError{Path: "/tank/big.bin", Size: 10, Capacity: 5},
			"больше бюджета кассеты",
			&domain.FileTooLargeError{},
		},
		{
			"указатель продолжения",
			&domain.ContinuationError{JobRunID: "run", SessionNum: 7, Part: 2, NextTapeName: "media-014"},
			`на кассете "media-014"`,
			&domain.ContinuationError{},
		},
		{
			"не указатель продолжения",
			&domain.NotContinuationError{Snippet: "NIL_BACKUP_TAPE"},
			"не является указателем продолжения",
			&domain.NotContinuationError{},
		},
		{
			"смена кассет недоступна",
			&domain.TapeChangerError{},
			"spanning требует интерактивной смены кассет или демона",
			&domain.TapeChangerError{},
		},
		{
			"кассета не подходит цепочке",
			&domain.ChainMismatchError{Expected: "media-014", Got: "media-099"},
			`ожидалось "media-014", найдено "media-099"`,
			&domain.ChainMismatchError{},
		},
		{
			"пустой индекс сессии",
			&domain.EmptyIndexError{},
			"индекс сессии пуст",
			&domain.EmptyIndexError{},
		},
		{
			"нет кассеты в приводе",
			&domain.NoMediumError{},
			"нет кассеты в приводе",
			&domain.NoMediumError{},
		},
		{
			"повреждение сессии",
			&domain.SessionDamageError{},
			"сессия повреждена",
			&domain.SessionDamageError{},
		},
		{
			"сессия не читается обратно",
			&domain.VerifyError{SessionNum: 3, Details: "файл повреждён"},
			"сессия 3 записана, но не читается обратно: файл повреждён",
			&domain.VerifyError{},
		},
		{
			"сессия не читается обратно, потерян файл",
			&domain.VerifyError{SessionNum: 2, File: "/tank/big.bin", Details: "состав не совпал"},
			`файл "/tank/big.bin": состав не совпал`,
			&domain.VerifyError{},
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

	var cont *domain.ContinuationError
	if !errors.As(fmt.Errorf("чтение: %w",
		&domain.ContinuationError{NextTapeName: "media-014", Part: 2}), &cont) {
		t.Fatal("errors.As не извлёк ContinuationError")
	}
	if cont.NextTapeName != "media-014" || cont.Part != 2 {
		t.Errorf("ContinuationError = %+v, want {NextTapeName:media-014 Part:2}", cont)
	}

	var mm *domain.ChainMismatchError
	if !errors.As(fmt.Errorf("сверка: %w",
		&domain.ChainMismatchError{Expected: "media-014", Got: "media-099"}), &mm) {
		t.Fatal("errors.As не извлёк ChainMismatchError")
	}
	if mm.Expected != "media-014" || mm.Got != "media-099" {
		t.Errorf("ChainMismatchError = %+v, want {Expected:media-014 Got:media-099}", mm)
	}

	var ve *domain.VerifyError
	if !errors.As(fmt.Errorf("backup: %w",
		&domain.VerifyError{SessionNum: 5, File: "/x"}), &ve) {
		t.Fatal("errors.As не извлёк VerifyError")
	}
	if ve.SessionNum != 5 || ve.File != "/x" {
		t.Errorf("VerifyError = %+v, want {SessionNum:5 File:/x}", ve)
	}
}
