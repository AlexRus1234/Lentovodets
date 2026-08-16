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

package tapeformat_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
)

func fixtureLabel() domain.TapeLabel {
	return domain.TapeLabel{
		Magic:         domain.Magic,
		FormatVersion: domain.FormatVersion,
		Name:          "media-001",
		UUID:          "550e8400-e29b-41d4-a716-446655440000",
		FormattedAt:   "2026-08-13T12:34:56Z",
	}
}

func TestEncodeLabel_PadsToBlockSize(t *testing.T) {
	block, err := tapeformat.EncodeLabel(fixtureLabel())
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	if len(block) != domain.BlockSize {
		t.Fatalf("len = %d, want %d", len(block), domain.BlockSize)
	}
	var label domain.TapeLabel
	if err := json.Unmarshal(bytes.TrimRight(block, "\x00"), &label); err != nil {
		t.Fatalf("ярлык не парсится из блока: %v", err)
	}
	if label != fixtureLabel() {
		t.Errorf("roundtrip ярлыка: %+v", label)
	}
}

func TestEncodeLabel_TooBigForBlock(t *testing.T) {
	big := fixtureLabel()
	big.Name = strings.Repeat("x", domain.BlockSize)
	if _, err := tapeformat.EncodeLabel(big); err == nil {
		t.Error("ярлык больше блока: want error")
	}
}

func TestDecodeLabel_Table(t *testing.T) {
	tests := []struct {
		name    string
		block   func() []byte
		wantErr error
		wantMsg string
	}{
		{
			name:    "пустая лента — все нули",
			block:   func() []byte { return make([]byte, domain.BlockSize) },
			wantErr: &domain.BlankTapeError{},
		},
		{
			name:    "пустая лента — пустой блок",
			block:   func() []byte { return nil },
			wantErr: &domain.BlankTapeError{},
		},
		{
			name: "не-JSON мусор",
			block: func() []byte {
				b := make([]byte, domain.BlockSize)
				copy(b, "\x01GARBAGE GARBAGE GARBAGE GARBAGE GARBAGE GARBAGE \x02")
				return b
			},
			wantErr: &domain.ForeignFormatError{},
			wantMsg: "GARBAGE",
		},
		{
			name: "чужой magic (legacy nil-backup)",
			block: func() []byte {
				b, _ := json.Marshal(domain.TapeLabel{
					Magic: "NIL_BACKUP_TAPE", FormatVersion: 1,
					Name: "old", UUID: "u", FormattedAt: "2020-01-01T00:00:00Z",
				})
				return b
			},
			wantErr: &domain.ForeignFormatError{},
		},
		{
			name: "наше семейство, будущая версия magic",
			block: func() []byte {
				b, _ := json.Marshal(domain.TapeLabel{
					Magic: "LENTOVODEC_TAPE_V99", FormatVersion: 99,
					Name: "x", UUID: "u", FormattedAt: "2026-01-01T00:00:00Z",
				})
				return b
			},
			wantErr: &domain.NewerFormatError{},
		},
		{
			name: "текущий magic, новее format_version",
			block: func() []byte {
				l := fixtureLabel()
				l.FormatVersion = domain.FormatVersion + 1
				b, _ := json.Marshal(l)
				return b
			},
			wantErr: &domain.NewerFormatError{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, err := tapeformat.DecodeLabel(tt.block())
			if err == nil {
				t.Fatalf("err = nil, label = %+v", label)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %T", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %v, want вхождение %q", err, tt.wantMsg)
			}
			var newer *domain.NewerFormatError
			if errors.As(err, &newer) {
				if newer.Supported != domain.FormatVersion {
					t.Errorf("Supported = %d, want %d", newer.Supported, domain.FormatVersion)
				}
			}
		})
	}
}

func TestDecodeLabel_OlderVersionIsReadable(t *testing.T) {
	old := fixtureLabel()
	old.FormatVersion = 1
	block, err := tapeformat.EncodeLabel(old)
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	label, err := tapeformat.DecodeLabel(block)
	if err != nil {
		t.Fatalf("DecodeLabel старой версии: %v", err)
	}
	if label != old {
		t.Errorf("label = %+v, want %+v", label, old)
	}
}
