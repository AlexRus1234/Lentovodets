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
	"strings"
	"testing"

	"lentovodec/internal/domain"
)

func TestJobModeValid(t *testing.T) {
	tests := []struct {
		mode domain.JobMode
		want bool
	}{
		{domain.ModeAppend, true},
		{domain.ModeMirror, true},
		{domain.JobMode(""), false},
		{domain.JobMode("sync"), false},
		{domain.JobMode("APPEND"), false},
	}
	for _, tt := range tests {
		if got := tt.mode.Valid(); got != tt.want {
			t.Errorf("JobMode(%q).Valid() = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestJobValidate(t *testing.T) {
	valid := domain.Job{
		Name:        "media",
		Description: "Бекап сериалов",
		Mode:        domain.ModeMirror,
		Paths:       []string{"/tank/data/media"},
		Exclude:     []string{"**/.DS_Store"},
	}

	tests := []struct {
		name    string
		mutate  func(*domain.Job)
		wantErr bool
	}{
		{"валид", func(*domain.Job) {}, false},
		{"пустое имя", func(j *domain.Job) { j.Name = "" }, true},
		{"пустой режим", func(j *domain.Job) { j.Mode = "" }, true},
		{"чужой режим", func(j *domain.Job) { j.Mode = domain.JobMode("sync") }, true},
		{"нет путей", func(j *domain.Job) { j.Paths = nil }, true},
		{"пустой список путей", func(j *domain.Job) { j.Paths = []string{} }, true},
		{"пробельный путь", func(j *domain.Job) { j.Paths = []string{"/ok", "   "} }, true},
		{"append валиден", func(j *domain.Job) { j.Mode = domain.ModeAppend }, false},
		{"span_depth ноль валиден", func(j *domain.Job) { j.SpanDepth = 0 }, false},
		{"span_depth положительная валидна", func(j *domain.Job) { j.SpanDepth = 3 }, false},
		{"отрицательная span_depth", func(j *domain.Job) { j.SpanDepth = -1 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := valid
			tt.mutate(&job)
			err := job.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Job.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestJobValidateModeMentionsValue(t *testing.T) {
	job := domain.Job{Name: "x", Mode: domain.JobMode("sync"), Paths: []string{"/a"}}
	err := job.Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка валидации режима")
	}
	for _, want := range []string{"sync", "append", "mirror", "x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ошибка %q не упоминает %q", err, want)
		}
	}
}
