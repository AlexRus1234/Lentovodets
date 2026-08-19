// Лентоводец — система резервного копирования на ленточные накопители LTO
// Copyright (C) 2026 AlexRus1234

package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

func TestBackupRestore_SpecialFilesStructure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink and inode semantics are environment-dependent on Windows")
	}
	h := newHarness(t)
	h.addJob(domain.Job{Name: "special", Mode: domain.ModeAppend, Paths: []string{h.src}})
	first := filepath.Join(h.src, "first.txt")
	second := filepath.Join(h.src, "second.txt")
	sym := filepath.Join(h.src, "dangling")
	if err := os.WriteFile(first, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", sym); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := h.formatUC().Format(context.Background(), "SPECIAL", false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.backupUC().Backup(context.Background(), "special", backup.Options{}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.restoreUC().Full(context.Background()); err != nil {
		t.Fatalf("restore full: %v", err)
	}
	dst := destRoot(h.dest, h.src)
	gotSym := filepath.Join(dst, "dangling")
	info, err := os.Lstat(gotSym)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("restored dangling symlink: info=%v err=%v", info, err)
	}
	if target, err := os.Readlink(gotSym); err != nil || target != "missing-target" {
		t.Fatalf("restored symlink target = %q, err=%v", target, err)
	}
	firstInfo, err := os.Stat(filepath.Join(dst, "first.txt"))
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(filepath.Join(dst, "second.txt"))
	if err != nil || !os.SameFile(firstInfo, secondInfo) {
		t.Fatalf("restored hardlink identity: first=%v second=%v same=%v", firstInfo, secondInfo, err == nil && os.SameFile(firstInfo, secondInfo))
	}

	selectiveDest := h.dest + "-selective"
	if _, err := h.restoreUCTo(selectiveDest).Selective(context.Background(), 1, []string{sym}); err != nil {
		t.Fatalf("selective symlink restore: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destRoot(selectiveDest, h.src), "dangling")); err != nil {
		t.Fatalf("selective symlink missing: %v", err)
	}

	// Selective-восстановление hardlink-пары: восстановленный участник
	// остаётся жёсткой ссылкой (владелец извлекается из той же сессии).
	hardDest := h.dest + "-hardlink"
	if _, err := h.restoreUCTo(hardDest).Selective(context.Background(), 1, []string{second}); err != nil {
		t.Fatalf("selective hardlink restore: %v", err)
	}
	hd := destRoot(hardDest, h.src)
	hFirst, err := os.Stat(filepath.Join(hd, "first.txt"))
	if err != nil {
		t.Fatal(err)
	}
	hSecond, err := os.Stat(filepath.Join(hd, "second.txt"))
	if err != nil || !os.SameFile(hFirst, hSecond) {
		t.Fatalf("selective hardlink identity: first=%v second=%v err=%v", hFirst, hSecond, err)
	}
}
