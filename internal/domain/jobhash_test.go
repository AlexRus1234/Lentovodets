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
	"encoding/hex"
	"strings"
	"testing"

	"lentovodec/internal/domain"
)

func snapJob() domain.Job {
	return domain.Job{
		Name:      "media",
		Mode:      domain.ModeMirror,
		Paths:     []string{"/tank/media", "/tank/docs"},
		Exclude:   []string{"*.tmp"},
		SpanDepth: 2,
	}
}

// TestJobConfigHash_ValueStable — хеш зависит от значений, а не от
// памяти под срезами конфигурации.
func TestJobConfigHash_ValueStable(t *testing.T) {
	a := snapJob()
	b := snapJob()
	b.Paths = append([]string(nil), a.Paths...)
	b.Exclude = append([]string(nil), a.Exclude...)
	if domain.JobConfigHash(a) != domain.JobConfigHash(b) {
		t.Error("хеш изменился при том же значении задания")
	}
}

// TestJobConfigHash_CoversFields — каждое поле задания входит в форму.
func TestJobConfigHash_CoversFields(t *testing.T) {
	base := snapJob()
	want := domain.JobConfigHash(base)
	mutants := map[string]func(*domain.Job){
		"name":        func(j *domain.Job) { j.Name += "x" },
		"description": func(j *domain.Job) { j.Description = "описание" },
		"mode":        func(j *domain.Job) { j.Mode = domain.ModeAppend },
		"paths_count": func(j *domain.Job) { j.Paths = j.Paths[:1] },
		"paths_value": func(j *domain.Job) { j.Paths[1] += "x" },
		"paths_order": func(j *domain.Job) { j.Paths[0], j.Paths[1] = j.Paths[1], j.Paths[0] },
		"exclude":     func(j *domain.Job) { j.Exclude = nil },
		"span_depth":  func(j *domain.Job) { j.SpanDepth++ },
	}
	for name, mutate := range mutants {
		j := base
		j.Paths = append([]string(nil), base.Paths...)
		j.Exclude = append([]string(nil), base.Exclude...)
		mutate(&j)
		if got := domain.JobConfigHash(j); got == want {
			t.Errorf("%s: хеш не изменился", name)
		}
	}
}

// TestJobConfigHash_HexSHA256 — 64 hex-символа lowercase: сверка
// sha256sum по каноническим байтам на спасательной машине.
func TestJobConfigHash_HexSHA256(t *testing.T) {
	h := domain.JobConfigHash(snapJob())
	if len(h) != 64 {
		t.Fatalf("длина %d; want 64", len(h))
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Fatalf("не hex: %v", err)
	}
}

// TestCanonicalJobConfig_Prefix — форма начинается с доменного
// префикса правила (версия канона различима читателем).
func TestCanonicalJobConfig_Prefix(t *testing.T) {
	b := domain.CanonicalJobConfig(snapJob())
	if !strings.HasPrefix(string(b), "lentovodec.jobconfig.canon1\n") {
		t.Errorf("форма без префикса правила: %q", b[:40])
	}
}
