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

func TestFileMetaValidateSpecialTypes(t *testing.T) {
	tests := []struct {
		name    string
		meta    domain.FileMeta
		wantErr string
	}{
		{name: "old regular", meta: domain.FileMeta{Path: "/old"}},
		{name: "regular linkname", meta: domain.FileMeta{Path: "/f", Linkname: "/target"}, wantErr: "regular file"},
		{name: "symlink without target", meta: domain.FileMeta{Path: "/link", Type: domain.FileTypeSymlink}, wantErr: "no linkname"},
		{name: "hardlink without target", meta: domain.FileMeta{Path: "/link", Type: domain.FileTypeHardlink}, wantErr: "no linkname"},
		{name: "unknown type", meta: domain.FileMeta{Path: "/f", Type: "fifo"}, wantErr: "invalid type"},
		{name: "symlink", meta: domain.FileMeta{Path: "/link", Type: domain.FileTypeSymlink, Linkname: "missing"}},
		{name: "hardlink", meta: domain.FileMeta{Path: "/link", Type: domain.FileTypeHardlink, Linkname: "/first"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.meta.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestFileMetaOldTypeMeansRegular(t *testing.T) {
	fm := domain.FileMeta{Path: "/old", Linkname: ""}
	if fm.IsSymlink() || fm.IsHardlink() || !fm.ValidType() {
		t.Fatalf("old metadata interpreted incorrectly: %+v", fm)
	}
}

func TestFileTypeNormalized(t *testing.T) {
	tests := []struct {
		in   domain.FileType
		want domain.FileType
	}{
		{"", domain.FileTypeRegular},
		{domain.FileTypeRegular, domain.FileTypeRegular},
		{domain.FileTypeSymlink, domain.FileTypeSymlink},
		{domain.FileTypeHardlink, domain.FileTypeHardlink},
	}
	for _, tt := range tests {
		if got := tt.in.Normalized(); got != tt.want {
			t.Errorf("FileType(%q).Normalized() = %q, want %q", tt.in, got, tt.want)
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
