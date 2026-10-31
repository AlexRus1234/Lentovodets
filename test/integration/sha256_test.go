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

// Per-file SHA-256 (ишью №4): полный круг на реальном кодеке, filetape
// и osfs — сканер считает оба хеша, индекс несёт sha256, restore
// сверяет содержимое по sha256, каталог хранит колонку.

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

func TestPerFileSHA256_FullCircle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const content = "контент для сквозного sha256"
	writeFile(t, h.src, "a.bin", []byte(content))
	file := filepath.Join(h.src, "a.bin")
	h.addJob(domain.Job{Name: "sha", Mode: domain.ModeMirror, Paths: []string{h.src}})
	label, err := h.formatUC().Format(ctx, "LTO-SHA", false)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if _, err := h.backupUC().Backup(ctx, "sha", backup.Options{}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// индекс на ленте несёт sha256 содержимого
	if err := h.tape.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if err := h.tape.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("ForwardFilemarks(1): %v", err)
	}
	header, files, err := h.codec.ReadIndexFiles(ctx, h.tape)
	if err != nil {
		t.Fatalf("ReadIndexFiles: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	want := hex.EncodeToString(sum[:])
	var found bool
	for _, fm := range files {
		if fm.Path == file && !fm.IsDir {
			found = true
			if fm.SHA256 != want {
				t.Errorf("SHA256 индекса %q; want %q", fm.SHA256, want)
			}
			if fm.Hash == "" {
				t.Error("xxhash64 отсутствует у записи с sha256")
			}
		}
	}
	if !found {
		t.Fatalf("файл %s не найден в индексе: %+v", file, header)
	}

	// каталог хранит колонку
	h.reopen()
	sessions, err := h.cat.ListSessions(ctx, label.UUID)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("ListSessions: %v, %d", err, len(sessions))
	}
	cat, err := h.cat.GetFilesBySession(ctx, sessions[0].ID)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	for _, fm := range cat {
		if fm.Path == file && fm.SHA256 != want {
			t.Errorf("SHA256 каталога %q; want %q", fm.SHA256, want)
		}
	}

	// restore сверяет содержимое по sha256 (новые ленты)
	if _, err := h.restoreUC().Full(ctx); err != nil {
		t.Fatalf("restore full: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(h.dest, file))
	if err != nil || string(got) != content {
		t.Fatalf("восстановлено: %q, %v", got, err)
	}
}
