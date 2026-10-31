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

// Снапшот конфигурации задания в индексе сессии (docs/FORMAT.md §5):
// полное тело в первой сессии кассеты, дедуп при неизменной
// конфигурации, полное тело при изменении. Реальный кодек и filetape:
// дедуп читает индекс предыдущей сессии с самой ленты.

package integration

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
)

// snapshotUC — backup.UseCase с версией инструмента для снапшота
// (harness-умолчание — пустая версия, снапшот-тестам нужна различимая).
func (h *harness) snapshotUC() *backup.UseCase {
	return backup.New(h.cfg, h.tape, h.codec, h.cat, h.fs, h.hasher,
		h.rnd, h.clock, nil, testutil.NoopLogger(), nil, "1.1.0-it")
}

// readSnapshot читает заголовок сессии num с ленты (индекс сессии K —
// за меткой 2K-1, docs/FORMAT.md §9).
func (h *harness) readSnapshot(num int32) (port.SessionHeader, error) {
	ctx := context.Background()
	if err := h.tape.Rewind(ctx); err != nil {
		return port.SessionHeader{}, err
	}
	if err := h.tape.ForwardFilemarks(ctx, int(2*num-1)); err != nil {
		return port.SessionHeader{}, err
	}
	header, _, err := h.codec.ReadIndexFiles(ctx, h.tape)
	return header, err
}

func TestSnapshot_FirstSessionCarriesFullBody(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	file := filepath.Join(h.src, "a.txt")
	if err := os.WriteFile(file, []byte("данные"), 0o644); err != nil {
		t.Fatalf("запись источника: %v", err)
	}
	job := domain.Job{Name: "snap", Mode: domain.ModeMirror, Paths: []string{h.src}, Exclude: []string{"*.tmp"}, SpanDepth: 1}
	h.addJob(job)
	if _, err := h.formatUC().Format(ctx, "LTO-SNAP", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	if _, err := h.snapshotUC().Backup(ctx, "snap", backup.Options{}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	header, err := h.readSnapshot(1)
	if err != nil {
		t.Fatalf("чтение индекса сессии 1: %v", err)
	}
	if header.JobConfig == nil {
		t.Fatal("первая сессия без полного тела снапшота")
	}
	if !reflect.DeepEqual(*header.JobConfig, job) {
		t.Errorf("тело: %+v; want %+v", *header.JobConfig, job)
	}
	if header.JobConfigHash != domain.JobConfigHash(job) {
		t.Error("job_config_hash не равен хешу канонической формы задания")
	}
	if header.ToolVersion != "1.1.0-it" {
		t.Errorf("tool_version = %q; want 1.1.0-it", header.ToolVersion)
	}
	if header.ConfigSource != "" || header.ConfigMTime != 0 {
		t.Errorf("config_source/mtime: %q/%d; want пусто (StaticConfig без файла)", header.ConfigSource, header.ConfigMTime)
	}
}

func TestSnapshot_DedupAndChange(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	file := filepath.Join(h.src, "a.txt")
	if err := os.WriteFile(file, []byte("данные"), 0o644); err != nil {
		t.Fatalf("запись источника: %v", err)
	}
	job := domain.Job{Name: "snap", Mode: domain.ModeMirror, Paths: []string{h.src}, Exclude: []string{"*.tmp"}}
	h.addJob(job)
	if _, err := h.formatUC().Format(ctx, "LTO-SNAP", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	if _, err := h.snapshotUC().Backup(ctx, "snap", backup.Options{}); err != nil {
		t.Fatalf("backup#1: %v", err)
	}
	h1, err := h.readSnapshot(1)
	if err != nil {
		t.Fatalf("индекс сессии 1: %v", err)
	}

	// та же конфигурация — сессия 2 без тела, хеш совпадает
	if _, err := h.snapshotUC().Backup(ctx, "snap", backup.Options{}); err != nil {
		t.Fatalf("backup#2: %v", err)
	}
	h2, err := h.readSnapshot(2)
	if err != nil {
		t.Fatalf("индекс сессии 2: %v", err)
	}
	if h2.JobConfig != nil {
		t.Fatalf("неизменная конфигурация: тело записано: %+v", *h2.JobConfig)
	}
	if h2.JobConfigHash != h1.JobConfigHash {
		t.Errorf("хеш дедуп-сессии %q != хешу первой %q", h2.JobConfigHash, h1.JobConfigHash)
	}

	// изменённый exclude — полное тело с новым хешем
	h.cfg.JobList[0].Exclude = append(h.cfg.JobList[0].Exclude, "*.bak")
	if _, err := h.snapshotUC().Backup(ctx, "snap", backup.Options{}); err != nil {
		t.Fatalf("backup#3: %v", err)
	}
	h3, err := h.readSnapshot(3)
	if err != nil {
		t.Fatalf("индекс сессии 3: %v", err)
	}
	if h3.JobConfig == nil {
		t.Fatal("изменённая конфигурация: тело не записано")
	}
	if h3.JobConfigHash == h1.JobConfigHash {
		t.Error("хеш не изменился вместе с конфигурацией")
	}
	if !reflect.DeepEqual(*h3.JobConfig, h.cfg.JobList[0]) {
		t.Errorf("тело: %+v; want %+v", *h3.JobConfig, h.cfg.JobList[0])
	}
}
