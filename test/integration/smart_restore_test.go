// Сценарии smart-restore: несколько копий файла на ленте, повреждённая
// копия, fallback на более старую (docs/ROADMAP.md Этап 9).

package integration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

const (
	smartV1 = "smart report v1 — альфа-перезапись "
	smartV2 = "smart report v2 — бета-перезапись "
)

// smartHarness — лента с двумя сессиями: report.txt лежит в обеих
// (v1 и v2), other.txt — только во второй.
func smartHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-S01", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "report.txt", []byte(strings.Repeat(smartV1, 20)))
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("backup #1: %v", err)
	}

	writeFile(t, h.src, "report.txt", []byte(strings.Repeat(smartV2, 20)))
	writeFile(t, h.src, "notes/other.txt", []byte("other v2\n"))
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("backup #2: %v", err)
	}
	return h
}

// TestSmartRestore_LatestCopyWins — обе копии читаются, восстановление
// идёт из самой свежей сессии.
func TestSmartRestore_LatestCopyWins(t *testing.T) {
	h := smartHarness(t)
	h.reopen() // свежие указатели ленты/каталога — как новый процесс
	ctx := context.Background()

	st, err := h.restoreUC().Smart(ctx, []string{
		filepath.Join(h.src, "report.txt"),
		filepath.Join(h.src, "notes", "other.txt"),
	})
	if err != nil {
		t.Fatalf("smart: %v", err)
	}
	if st.Files != 2 {
		t.Fatalf("stats: %+v, хочу files=2", st)
	}
	root := destRoot(h.dest, h.src)
	files, _ := treeOf(t, root)
	if got := string(files["report.txt"]); got != strings.Repeat(smartV2, 20) {
		t.Error("report.txt: хочу самую свежую копию (v2)")
	}
	if got := files["notes/other.txt"]; string(got) != "other v2\n" {
		t.Errorf("other.txt: %q, хочу other v2", got)
	}
}

// TestSmartRestore_FallbackOnCorruptedCopy — свежая копия повреждена
// (байт в tar-потоке изменён, хеш не сходится): smart откатывается к
// более старой читаемой копии.
func TestSmartRestore_FallbackOnCorruptedCopy(t *testing.T) {
	h := smartHarness(t)
	ctx := context.Background()

	if err := h.tape.Close(); err != nil {
		t.Fatalf("закрытие ленты: %v", err)
	}
	corruptNeedle(t, h.tapePath, smartV2)
	h.openTape()

	st, err := h.restoreUC().Smart(ctx, []string{filepath.Join(h.src, "report.txt")})
	if err != nil {
		t.Fatalf("smart с повреждённой копией: %v", err)
	}
	if st.Files != 1 {
		t.Fatalf("stats: %+v, хочу files=1", st)
	}
	files, _ := treeOf(t, destRoot(h.dest, h.src))
	if got := string(files["report.txt"]); got != strings.Repeat(smartV1, 20) {
		t.Error("report.txt: fallback на старую копию не сработал")
	}
}

// TestSmartRestore_NoHealthyCopy — обе копии повреждены: типизированная
// ошибка NoHealthyCopyError. Путь без копий вовсе — та же ошибка.
func TestSmartRestore_NoHealthyCopy(t *testing.T) {
	h := smartHarness(t)
	ctx := context.Background()

	if err := h.tape.Close(); err != nil {
		t.Fatalf("закрытие ленты: %v", err)
	}
	corruptNeedle(t, h.tapePath, smartV1)
	corruptNeedle(t, h.tapePath, smartV2)
	h.openTape()

	_, err := h.restoreUC().Smart(ctx, []string{filepath.Join(h.src, "report.txt")})
	var noCopy *domain.NoHealthyCopyError
	if !errors.As(err, &noCopy) {
		t.Fatalf("smart с двумя повреждёнными копиями: err=%v, хочу NoHealthyCopyError", err)
	}

	_, err = h.restoreUC().Smart(ctx, []string{filepath.Join(h.src, "no-such-file.txt")})
	if !errors.As(err, &noCopy) {
		t.Fatalf("smart без копий: err=%v, хочу NoHealthyCopyError", err)
	}
}
