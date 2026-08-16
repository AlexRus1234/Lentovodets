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

//go:build tape && linux

// Сквозной сценарий на реальном стримере: format → backup → readtest →
// restore full → сравнение дерева (docs/ROADMAP.md Этап 9).
//
// Запуск (машина с приводом, пользователь в группе tape):
//
//	LENTOVODEC_TAPE_DEVICE=/dev/nst0 go test -tags=tape ./test/hardware/ -v -count=1 -run TestHardware_FormatBackupRestoreFull
//
// ВНИМАНИЕ: тест разрушает все данные на кассете. Для повторного
// форматирования уже размеченной ленты установите LENTOVODEC_TAPE_REFORMAT=1.

package hardware

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lentovodec/internal/adapter/osfs"
	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/adapter/xxhash"
	"lentovodec/internal/domain"
	"lentovodec/internal/iface/destfs"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
	"lentovodec/internal/usecase/catalog"
	"lentovodec/internal/usecase/format"
	"lentovodec/internal/usecase/restore"
)

// hwWriteFile создаёт файл root/rel с содержимым content.
func hwWriteFile(t *testing.T, root, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", rel, err)
	}
}

// hwTree собирает карту rel-путь('/') → содержимое для файлов дерева.
func hwTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		files[filepath.ToSlash(rel)] = raw
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// hwDestRoot — куда destfs переносит корень src под dest.
func hwDestRoot(dest, src string) string {
	slash := filepath.ToSlash(filepath.Clean(src))
	slash = strings.TrimPrefix(slash, filepath.VolumeName(slash))
	slash = strings.TrimPrefix(slash, "/")
	return filepath.Join(dest, filepath.FromSlash(slash))
}

// TestHardware_FormatBackupRestoreFull — полный цикл на реальной ленте:
// форматирование, бекап небольшого дерева, диагностическое чтение всей
// ленты (readtest) и восстановление в отдельный каталог с побайтовым
// сравнением.
func TestHardware_FormatBackupRestoreFull(t *testing.T) {
	tp := openTape(t)
	ctx := context.Background()

	base := t.TempDir()
	src := filepath.Join(base, "src")
	dest := filepath.Join(base, "dest")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll src: %v", err)
	}
	hwWriteFile(t, src, "readme.txt", []byte("hardware round-trip\n"))
	hwWriteFile(t, src, "sub/data.bin", bytes.Repeat([]byte{0xA5, 0x5A, 0x00, 0xFF}, 2048))

	fsys := osfs.New()
	codec := tapeformat.NewCodec()
	hasher := xxhash.New()
	var clock port.Clock = testutil.StepClock(time.Unix(1700000000, 0))
	rnd := testutil.FixedRand(
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	)
	cfg := &testutil.StaticConfig{JobList: []domain.Job{{
		Name:  "hw",
		Mode:  domain.ModeAppend,
		Paths: []string{src},
	}}}
	cat, err := sqlite.New(filepath.Join(base, "catalog.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	force := os.Getenv("LENTOVODEC_TAPE_REFORMAT") != ""
	t.Logf("format (force=%v)...", force)
	label, err := format.New(tp, codec, cat, rnd, clock, testutil.NoopLogger()).
		Format(ctx, "HW-001", force)
	if err != nil {
		var already *domain.AlreadyFormattedError
		if errors.As(err, &already) {
			t.Fatalf("лента уже отформатирована (%s): запустите с LENTOVODEC_TAPE_REFORMAT=1", already.Name)
		}
		t.Fatalf("format: %v", err)
	}
	t.Logf("formatted: uuid=%s", label.UUID)

	t.Log("backup...")
	res, err := backup.New(cfg, tp, codec, cat, fsys, hasher, rnd, clock, nil, testutil.NoopLogger()).
		Backup(ctx, "hw", backup.Options{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	t.Logf("backup: session %d (%s), files=%d bytes=%d",
		res.Session.Num, res.Session.Type, res.Stats.Scanned, res.Stats.Bytes)

	t.Log("readtest (полное чтение ленты со сверкой хешей)...")
	checked, err := catalog.New(cat, tp, codec, nil, testutil.NoopLogger()).ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest: %v", err)
	}
	if checked != 1 {
		t.Fatalf("readtest: сессий %d, хочу 1", checked)
	}

	t.Log("restore full...")
	st, err := restore.New(tp, codec, cat, destfs.Wrap(fsys, dest), nil, testutil.NoopLogger()).
		Full(ctx)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Logf("restore: sessions=%d files=%d dirs=%d", st.Sessions, st.Files, st.Dirs)

	want := hwTree(t, src)
	got := hwTree(t, hwDestRoot(dest, src))
	if len(got) != len(want) {
		t.Fatalf("восстановлено файлов %d, хочу %d: %v", len(got), len(want), got)
	}
	for p, wantContent := range want {
		if !bytes.Equal(got[p], wantContent) {
			t.Errorf("файл %s: содержимое не совпало", p)
		}
	}
	t.Log("OK: дерево восстановлено побайтово")
}
