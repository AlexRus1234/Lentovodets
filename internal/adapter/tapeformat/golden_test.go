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
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/testutil"
)

var update = flag.Bool("update", false, "перезаписать golden-файлы актуальными байтами")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// TestGoldenSessionSnapshot — индекс снапшот-сессии: полный объект
// job_config со всеми полями, источник конфигурации, версия
// инструмента; канон canon2. Отдельный golden держит wire-вид
// снапшота стабильным (docs/FORMAT.md §5).
func TestGoldenSessionSnapshot(t *testing.T) {
	fs := testutil.NewMapFS(map[string]string{"/data/a.txt": "a"})
	idx := tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    1,
		Type:          domain.SessionFull,
		JobRunID:      "0f4c2d6a-1111-4222-8333-444455556666",
		Timestamp:     1700000000,
		JobName:       "media",
		ToolVersion:   "1.1.0",
		ConfigSource:  "/etc/lentovodec.toml",
		ConfigMTime:   1699990000,
		JobConfigHash: domain.JobConfigHash(snapshotFixtureJob()),
		JobConfig: &tapeformat.JobSnapshot{
			Name:        "media",
			Description: "медиатека",
			Mode:        string(domain.ModeMirror),
			Paths:       []string{"/tank/media", "/tank/docs"},
			Exclude:     []string{"*.tmp", "*.bak"},
			SpanDepth:   2,
		},
		Files: []domain.FileMeta{{
			Path:    "/data/a.txt",
			Size:    1,
			ModTime: 1700000001,
			Hash:    hashOf("a"),
			SHA256:  sha256Of("a"),
			State:   domain.StateAdded,
		}},
	}
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs)
	blocks, _ := tape.Snapshot()
	checkGolden(t, "index_snapshot.bin", blocks[0])
}

// snapshotFixtureJob — задание, зеркалящее JobSnapshot золотника.
func snapshotFixtureJob() domain.Job {
	return domain.Job{
		Name:        "media",
		Description: "медиатека",
		Mode:        domain.ModeMirror,
		Paths:       []string{"/tank/media", "/tank/docs"},
		Exclude:     []string{"*.tmp", "*.bak"},
		SpanDepth:   2,
	}
}

// TestGoldenSessionSnapshotVerified — записанные золотные байты
// читаются реальным путём (readIndex: parse → verify canon2 → use):
// снапшот возвращается заголовком сессии целиком.
func TestGoldenSessionSnapshotVerified(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("golden", "index_snapshot.bin"))
	if err != nil {
		t.Fatalf("чтение index_snapshot.bin: %v", err)
	}
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	if err := tape.WriteBlock(ctx, raw); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	if err := tape.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	header, err := tapeformat.ReadHeader(ctx, tape)
	if err != nil {
		t.Fatalf("ReadHeader золотника: %v", err)
	}
	if header.JobConfig == nil || header.JobConfig.Name != "media" {
		t.Fatalf("снапшот не прочитан: %+v", header.JobConfig)
	}
	if header.JobConfigHash != domain.JobConfigHash(snapshotFixtureJob()) {
		t.Error("job_config_hash не равен хешу канонической формы тела")
	}
	if header.ToolVersion != "1.1.0" ||
		header.ConfigSource != "/etc/lentovodec.toml" ||
		header.ConfigMTime != 1699990000 {
		t.Errorf("поля источника: %+v", header)
	}
}

func TestGoldenLabel(t *testing.T) {
	block, err := tapeformat.EncodeLabel(fixtureLabel())
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	checkGolden(t, "label.bin", block)
}

func TestGoldenSession(t *testing.T) {
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs)

	blocks, marks := tape.Snapshot()
	checkGolden(t, "index.bin", blocks[0])

	var all []byte
	for _, b := range blocks {
		all = append(all, b...)
	}
	checkGolden(t, "session.bin", all)

	var sb strings.Builder
	for _, m := range marks {
		fmt.Fprintf(&sb, "%d\n", m)
	}
	checkGolden(t, "session_marks.txt", []byte(sb.String()))
}

func TestGoldenDeterministic(t *testing.T) {
	record := func() []byte {
		fs, idx := buildFixture(t)
		tape := testutil.NewFakeTape()
		writeFixtureSession(t, tape, idx, fs)
		blocks, _ := tape.Snapshot()
		var all []byte
		for _, b := range blocks {
			all = append(all, b...)
		}
		return all
	}
	if !bytes.Equal(record(), record()) {
		t.Error("две записи одной сессии дают разные байты")
	}
}

// checkGolden сравнивает байты с golden-файлом; при -update перезаписывает.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("golden", name)
	if *update {
		if err := os.MkdirAll("golden", 0o755); err != nil {
			t.Fatalf("MkdirAll golden: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("запись %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s (если формат менялся осознанно: go test ./internal/adapter/tapeformat -update): %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s: байты отличаются (want %d Б, got %d Б); если формат менялся осознанно: -update", path, len(want), len(got))
	}
}
