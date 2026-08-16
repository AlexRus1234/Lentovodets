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

// Общий харнесс интеграционных тестов: реальные адаптеры (filetape +
// osfs + sqlite + tapeformat + xxhash) и собранные на них use case'ы.
// Фейки не используются — проверяется склейка слоёв целиком.
// См. docs/ROADMAP.md Этап 9, docs/TESTING.md §2.

package integration

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lentovodec/internal/adapter/filetape"
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

// harness — набор реальных зависимостей одного сценария. Каждый тест
// получает свои временные каталоги; «извлечение кассеты» эмулируется
// закрытием и повторным открытием ленты и каталога (reopen).
type harness struct {
	t        *testing.T
	src      string // корень данных задания
	dest     string // куда восстанавливает restoreUC
	tapePath string
	dbPath   string

	// tapeCapacity > 0 — открывать filetape с лимитом ёмкости
	// (OpenCapacity, сценарии «кончилась лента»); применяется при
	// очередном openTape/reopen.
	tapeCapacity int64

	fs     *osfs.FS
	codec  *tapeformat.Codec
	hasher *xxhash.Hasher
	cfg    *testutil.StaticConfig
	clock  port.Clock
	rnd    port.Rand

	tape *filetape.Tape
	cat  *sqlite.Catalog
}

// newHarness создаёт харнесс; задания добавляются addJob (им нужен
// уже созданный путь источника).
func newHarness(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	h := &harness{
		t:        t,
		src:      filepath.Join(base, "src"),
		dest:     filepath.Join(base, "dest"),
		tapePath: filepath.Join(base, "tape.dat"),
		dbPath:   filepath.Join(base, "catalog.db"),
		fs:       osfs.New(),
		codec:    tapeformat.NewCodec(),
		hasher:   xxhash.New(),
		cfg:      &testutil.StaticConfig{},
		clock:    testutil.StepClock(time.Unix(1700000000, 0)),
		rnd: testutil.FixedRand(
			"11111111-1111-4111-8111-111111111111",
			"22222222-2222-4222-8222-222222222222",
			"33333333-3333-4333-8333-333333333333",
			"44444444-4444-4444-8444-444444444444",
		),
	}
	if err := os.MkdirAll(h.src, 0o755); err != nil {
		t.Fatalf("каталог источника %s: %v", h.src, err)
	}
	h.openTape()
	h.openCatalog()
	t.Cleanup(func() {
		if h.tape != nil {
			_ = h.tape.Close()
		}
		if h.cat != nil {
			_ = h.cat.Close()
		}
	})
	return h
}

// addJob добавляет задание в конфигурацию (для backup.UseCase).
func (h *harness) addJob(job domain.Job) {
	h.t.Helper()
	job.Paths = append([]string(nil), job.Paths...)
	h.cfg.JobList = append(h.cfg.JobList, job)
}

// openTape открывает (или переоткрывает) файл-ленту; при заданном
// tapeCapacity — с лимитом ёмкости.
func (h *harness) openTape() {
	h.t.Helper()
	var (
		tp  *filetape.Tape
		err error
	)
	if h.tapeCapacity > 0 {
		tp, err = filetape.OpenCapacity(h.tapePath, h.tapeCapacity)
	} else {
		tp, err = filetape.Open(h.tapePath)
	}
	if err != nil {
		h.t.Fatalf("filetape.Open: %v", err)
	}
	h.tape = tp
}

// openCatalog открывает (или переоткрывает) sqlite-каталог.
func (h *harness) openCatalog() {
	h.t.Helper()
	cat, err := sqlite.New(h.dbPath)
	if err != nil {
		h.t.Fatalf("sqlite.New: %v", err)
	}
	h.cat = cat
}

// reopen имитирует извлечение кассеты и перезапуск утилиты: лента и
// каталог закрываются и открываются заново. Лента встаёт в BOT, каталог
// читается с диска — заодно проверяется персистентность sqlite.
func (h *harness) reopen() {
	h.t.Helper()
	if err := h.tape.Close(); err != nil {
		h.t.Fatalf("закрытие ленты: %v", err)
	}
	if err := h.cat.Close(); err != nil {
		h.t.Fatalf("закрытие каталога: %v", err)
	}
	h.tape, h.cat = nil, nil
	h.openTape()
	h.openCatalog()
}

// formatUC собирает use case форматирования на текущих зависимостях.
func (h *harness) formatUC() *format.UseCase {
	return format.New(h.tape, h.codec, h.cat, h.rnd, h.clock, testutil.NoopLogger())
}

// backupUC собирает use case бекапа (источник — реальная ФС).
func (h *harness) backupUC() *backup.UseCase {
	return backup.New(h.cfg, h.tape, h.codec, h.cat, h.fs, h.hasher,
		h.rnd, h.clock, nil, testutil.NoopLogger())
}

// restoreUC собирает use case восстановления в h.dest.
func (h *harness) restoreUC() *restore.UseCase {
	return h.restoreUCTo(h.dest)
}

// restoreUCTo собирает use case восстановления в каталог dest (как CLI
// restore --dest: перенос путей индекса под dest через destfs).
func (h *harness) restoreUCTo(dest string) *restore.UseCase {
	return restore.New(h.tape, h.codec, h.cat, destfs.Wrap(h.fs, dest),
		nil, testutil.NoopLogger())
}

// catalogUC собирает каталожный use case с доступом к ленте.
func (h *harness) catalogUC() *catalog.UseCase {
	return catalog.New(h.cat, h.tape, h.codec, nil, testutil.NoopLogger())
}

// writeFile создаёт файл root/rel (каталоги по пути) с содержимым
// content; rel — путь через '/'.
func writeFile(t *testing.T, root, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll для %s: %v", rel, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("запись %s: %v", rel, err)
	}
}

// mkdir создаёт пустой каталог root/rel.
func mkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", rel, err)
	}
}

// treeOf собирает дерево root: файлы (путь через '/' → содержимое) и
// множество каталогов (без корня).
func treeOf(t *testing.T, root string) (map[string][]byte, map[string]bool) {
	t.Helper()
	files := make(map[string][]byte)
	dirs := make(map[string]bool)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if slash == "." {
			return nil
		}
		if d.IsDir() {
			dirs[slash] = true
			return nil
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		files[slash] = raw
		return nil
	})
	if err != nil {
		t.Fatalf("обход дерева %s: %v", root, err)
	}
	return files, dirs
}

// destRoot вычисляет, куда destfs переносит корень src: путь без имени
// тома и ведущего '/' присоединяется к dest.
func destRoot(dest, src string) string {
	slash := filepath.ToSlash(filepath.Clean(src))
	slash = strings.TrimPrefix(slash, filepath.VolumeName(slash))
	slash = strings.TrimPrefix(slash, "/")
	return filepath.Join(dest, filepath.FromSlash(slash))
}

// assertTreeEqual сверяет дерево gotRoot с эталоном (файлы побайтово,
// каталоги по множеству; лишнее тоже ошибка).
func assertTreeEqual(t *testing.T, gotRoot string, wantFiles map[string][]byte, wantDirs map[string]bool) {
	t.Helper()
	gotFiles, gotDirs := treeOf(t, gotRoot)
	for p, want := range wantFiles {
		got, ok := gotFiles[p]
		if !ok {
			t.Errorf("файл %s отсутствует в восстановленном дереве", p)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("файл %s: содержимое не совпало (%d байт, хочу %d)", p, len(got), len(want))
		}
	}
	for p := range gotFiles {
		if _, ok := wantFiles[p]; !ok {
			t.Errorf("лишний файл %s в восстановленном дереве", p)
		}
	}
	for p := range wantDirs {
		if !gotDirs[p] {
			t.Errorf("каталог %s отсутствует в восстановленном дереве", p)
		}
	}
}

// corruptNeedle портит первый байт найденной подстроки needle в файле
// ленты — эмуляция повреждения носителя: framing файла-ленты не
// трогается, меняется только payload блока (хеш при чтении не сойдётся).
func corruptNeedle(t *testing.T, tapePath, needle string) {
	t.Helper()
	raw, err := os.ReadFile(tapePath)
	if err != nil {
		t.Fatalf("чтение файла ленты: %v", err)
	}
	i := bytes.Index(raw, []byte(needle))
	if i < 0 {
		t.Fatalf("подстрока %q не найдена в файле ленты", needle)
	}
	raw[i] ^= 0xFF
	if err := os.WriteFile(tapePath, raw, 0o600); err != nil {
		t.Fatalf("запись повреждённого файла ленты: %v", err)
	}
}
