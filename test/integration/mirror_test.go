// Сценарий mirror-режима: создание, изменение и удаление файлов;
// проверка состояний (включая tombstone'ы) после восстановления
// (docs/ROADMAP.md Этап 9).

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

// TestMirror_BackupRestoreReconstructsState — две mirror-сессии:
// вторая содержит Added/Modified и tombstone удалённого файла;
// restore full воспроизводит итоговое состояние источника (удалённый
// файл исчезает из dest — FORMAT §8).
func TestMirror_BackupRestoreReconstructsState(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "media", Mode: domain.ModeMirror, Paths: []string{h.src}})
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-M01", false); err != nil {
		t.Fatalf("format: %v", err)
	}

	// v1: дерево из четырёх файлов и пустого каталога
	writeFile(t, h.src, "a.txt", []byte("alpha v1\n"))
	writeFile(t, h.src, "sub/b.txt", []byte("bravo v1\n"))
	writeFile(t, h.src, "sub/c.txt", []byte("charlie v1\n"))
	writeFile(t, h.src, "keep/d.txt", []byte("delta\n"))
	mkdir(t, h.src, "logs")

	first, err := h.backupUC().Backup(ctx, "media", backup.Options{})
	if err != nil {
		t.Fatalf("backup #1: %v", err)
	}
	if first.Session.Type != domain.SessionFull {
		t.Fatalf("сессия #1: type=%s, хочу FULL", first.Session.Type)
	}
	if first.Stats.Deleted != 0 {
		t.Fatalf("сессия #1: deleted=%d, хочу 0", first.Stats.Deleted)
	}

	// v2: b.txt изменён, c.txt удалён, new.txt добавлен
	writeFile(t, h.src, "sub/b.txt", []byte("bravo v2 — содержимое перезаписано\n"))
	if err := os.Remove(filepath.Join(h.src, "sub", "c.txt")); err != nil {
		t.Fatalf("удаление c.txt: %v", err)
	}
	writeFile(t, h.src, "new.txt", []byte("новый файл\n"))

	second, err := h.backupUC().Backup(ctx, "media", backup.Options{})
	if err != nil {
		t.Fatalf("backup #2: %v", err)
	}
	// modified: sub/b.txt и, возможно, каталоги с изменившимся mtime
	// (Windows обновляет mtime каталогов лениво — число недетерминировано)
	if second.Stats.Added != 1 || second.Stats.Modified < 1 || second.Stats.Deleted != 1 {
		t.Fatalf("stats #2: %+v, хочу added=1 modified>=1 deleted=1", second.Stats)
	}

	// tombstone удалённого файла попал в сессию 2 каталога
	files2, err := h.catalogUC().GetFiles(ctx, second.Session.ID)
	if err != nil {
		t.Fatalf("файлы сессии 2: %v", err)
	}
	tombstones := map[string]bool{}
	for _, fm := range files2 {
		if fm.IsDeleted() {
			tombstones[fm.Path] = true
		}
	}
	if len(tombstones) != 1 || !hasSuffixPath(tombstones, "sub/c.txt") {
		t.Fatalf("tombstone'ы сессии 2: %v, хочу только sub/c.txt", tombstones)
	}
	// restore full: сессия 1 восстанавливает v1, сессия 2 накладывает
	// правки и удаляет c.txt
	st, err := h.restoreUC().Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	// файлы: 4 из сессии 1 + 2 из сессии 2 (b.txt, new.txt)
	if st.Sessions != 2 || st.Files != 6 {
		t.Fatalf("restore stats: %+v, хочу sessions=2 files=6", st)
	}

	root := destRoot(h.dest, h.src)
	gotFiles, gotDirs := treeOf(t, root)
	want := map[string]string{
		"a.txt":      "alpha v1\n",
		"sub/b.txt":  "bravo v2 — содержимое перезаписано\n",
		"keep/d.txt": "delta\n",
		"new.txt":    "новый файл\n",
	}
	for p, wantContent := range want {
		if got, ok := gotFiles[p]; !ok || string(got) != wantContent {
			t.Errorf("файл %s: %q (есть=%v), хочу %q", p, got, ok, wantContent)
		}
	}
	if _, ok := gotFiles["sub/c.txt"]; ok {
		t.Error("удалённый файл sub/c.txt восстановился, tombstone не сработал")
	}
	if !gotDirs["logs"] {
		t.Error("пустой каталог logs не восстановился из сессии 1")
	}
	if len(gotFiles) != 4 {
		t.Errorf("в dest %d файлов, хочу 4: %v", len(gotFiles), gotFiles)
	}
}

// hasSuffixPath сообщает, что в множестве есть путь, оканчивающийся на
// '/'+suffix или совпадающий с suffix.
func hasSuffixPath(set map[string]bool, suffix string) bool {
	for p := range set {
		if p == suffix || strings.HasSuffix(p, "/"+suffix) {
			return true
		}
	}
	return false
}
