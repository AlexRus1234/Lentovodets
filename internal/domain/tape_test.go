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
	"encoding/json"
	"strings"
	"testing"

	"lentovodec/internal/domain"
)

func TestFormatConstants(t *testing.T) {
	if domain.Magic != "LENTOVODEC_TAPE_V2" {
		t.Errorf("Magic = %q, want %q", domain.Magic, "LENTOVODEC_TAPE_V2")
	}
	if domain.FormatVersion != 2 {
		t.Errorf("FormatVersion = %d, want 2", domain.FormatVersion)
	}
	if domain.BlockSize != 256*1024 {
		t.Errorf("BlockSize = %d, want %d", domain.BlockSize, 256*1024)
	}
	if domain.CopyBuffer != 4*1024*1024 {
		t.Errorf("CopyBuffer = %d, want %d", domain.CopyBuffer, 4*1024*1024)
	}
}

func TestTapeLabelJSON(t *testing.T) {
	label := domain.TapeLabel{
		Magic:         domain.Magic,
		FormatVersion: domain.FormatVersion,
		Name:          "media-001",
		UUID:          "550e8400-e29b-41d4-a716-446655440000",
		FormattedAt:   "2026-08-13T12:34:56Z",
	}
	want := `{"magic":"LENTOVODEC_TAPE_V2","format_version":2,` +
		`"name":"media-001","uuid":"550e8400-e29b-41d4-a716-446655440000",` +
		`"formatted_at":"2026-08-13T12:34:56Z"}`

	got, err := json.Marshal(label)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("json.Marshal:\n got  %s\n want %s", got, want)
	}

	var back domain.TapeLabel
	if err := json.Unmarshal([]byte(want), &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if back != label {
		t.Errorf("round-trip: got %+v, want %+v", back, label)
	}
}

func TestTapeInfoConstruction(t *testing.T) {
	info := domain.TapeInfo{
		Label:    domain.TapeLabel{Name: "media-001"},
		Filemark: -1,
	}
	if info.Label.Name != "media-001" || info.Filemark != -1 {
		t.Errorf("TapeInfo = %+v", info)
	}
}

func TestTapeLabelParseFormattedAt(t *testing.T) {
	label := domain.TapeLabel{UUID: "u-1", FormattedAt: "2026-01-01T00:00:00Z"}
	ts, err := label.ParseFormattedAt()
	if err != nil {
		t.Fatalf("ParseFormattedAt: %v", err)
	}
	if ts.Unix() != 1767225600 {
		t.Errorf("Unix = %d, want 1767225600", ts.Unix())
	}

	for _, bad := range []string{"", "not-a-time", "2026-01-01"} {
		label.FormattedAt = bad
		if _, err := label.ParseFormattedAt(); err == nil {
			t.Errorf("formatted_at %q = nil, want ошибка", bad)
		} else if !strings.Contains(err.Error(), "u-1") {
			t.Errorf("ошибка %v не называет кассету", err)
		}
	}
}
