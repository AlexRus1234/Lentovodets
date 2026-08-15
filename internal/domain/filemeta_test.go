package domain_test

import (
	"encoding/json"
	"testing"

	"lentovodec/internal/domain"
)

func TestFileStateValid(t *testing.T) {
	tests := []struct {
		state domain.FileState
		want  bool
	}{
		{domain.StateAdded, true},
		{domain.StateModified, true},
		{domain.StateDeleted, true},
		{domain.FileState(""), false},
		{domain.FileState("X"), false},
		{domain.FileState("a"), false},
	}
	for _, tt := range tests {
		if got := tt.state.Valid(); got != tt.want {
			t.Errorf("FileState(%q).Valid() = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestFileMetaPredicates(t *testing.T) {
	tests := []struct {
		state      domain.FileState
		wantAdded  bool
		wantDelete bool
	}{
		{domain.StateAdded, true, false},
		{domain.StateModified, false, false},
		{domain.StateDeleted, false, true},
		{domain.FileState("X"), false, false},
	}
	for _, tt := range tests {
		fm := domain.FileMeta{State: tt.state}
		if got := fm.IsAdded(); got != tt.wantAdded {
			t.Errorf("FileMeta{State:%q}.IsAdded() = %v, want %v", tt.state, got, tt.wantAdded)
		}
		if got := fm.IsDeleted(); got != tt.wantDelete {
			t.Errorf("FileMeta{State:%q}.IsDeleted() = %v, want %v", tt.state, got, tt.wantDelete)
		}
	}
}

func TestFileMetaJSON(t *testing.T) {
	fm := domain.FileMeta{
		Path:    "/tank/data/media/movie.mkv",
		Size:    12345678,
		ModTime: 1691000000000000000,
		IsDir:   false,
		Hash:    "0123456789abcdef",
		State:   domain.StateModified,
	}
	want := `{"path":"/tank/data/media/movie.mkv","size":12345678,` +
		`"mod_time":1691000000000000000,"is_dir":false,` +
		`"hash":"0123456789abcdef","state":"M"}`

	got, err := json.Marshal(fm)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("json.Marshal:\n got  %s\n want %s", got, want)
	}

	var back domain.FileMeta
	if err := json.Unmarshal([]byte(want), &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if back != fm {
		t.Errorf("round-trip: got %+v, want %+v", back, fm)
	}
}
