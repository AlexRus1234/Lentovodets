package domain_test

import (
	"encoding/json"
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
