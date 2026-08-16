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

// Сценарий «format → backup → eject-simulated → read label → restore
// full → сравнить дерево файлов» через filetape + osfs + sqlite
// (docs/ROADMAP.md Этап 9).

package integration

import (
	"context"
	"path"
	"path/filepath"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

// TestBackupRestore_FormatBackupEjectRestoreFull — сквозной сценарий:
// форматирование пустой ленты, полный бекап с фильтром Exclude,
// «извлечение» кассеты (закрытие/переоткрытие ленты и каталога),
// чтение ярлыка заново установленной ленты, readtest и восстановление
// всего дерева в отдельный каталог.
func TestBackupRestore_FormatBackupEjectRestoreFull(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{
		Name:    "daily",
		Mode:    domain.ModeAppend,
		Paths:   []string{h.src},
		Exclude: []string{"*.tmp"},
	})
	ctx := context.Background()

	blob := make([]byte, 600*1024) // больше BlockSize — файл на несколько блоков
	for i := range blob {
		blob[i] = byte(i % 251)
	}
	writeFile(t, h.src, "docs/readme.md", []byte("# lentovodec integration\n"))
	writeFile(t, h.src, "docs/guides/deep.md", []byte("глубокий каталог, unicode\n"))
	writeFile(t, h.src, "media/blob.bin", blob)
	writeFile(t, h.src, "notes.txt", []byte("заметки на полях\n"))
	writeFile(t, h.src, "junk.tmp", []byte("должен быть исключён\n"))
	mkdir(t, h.src, "docs/scratch") // пустой каталог — тоже попадает в сессию

	// format
	label, err := h.formatUC().Format(ctx, "LTO-001", false)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if label.Magic != domain.Magic || label.FormatVersion != domain.FormatVersion {
		t.Fatalf("ярлык: magic=%q version=%d", label.Magic, label.FormatVersion)
	}
	if label.Name != "LTO-001" || label.UUID == "" || label.FormattedAt == "" {
		t.Fatalf("ярлык заполнен не полностью: %+v", label)
	}

	// backup (первая сессия → FULL)
	res, err := h.backupUC().Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if res.Session.Num != 1 || res.Session.Type != domain.SessionFull {
		t.Fatalf("сессия: num=%d type=%s, хочу 1/FULL", res.Session.Num, res.Session.Type)
	}
	wantFiles, wantDirs := treeOf(t, h.src)
	delete(wantFiles, "junk.tmp")
	var wantBytes int64
	for _, b := range wantFiles {
		wantBytes += int64(len(b))
	}
	wantEntries := len(wantFiles) + len(wantDirs) + 1 // + сам корень src
	if res.Stats.Added != wantEntries || res.Stats.Bytes != wantBytes {
		t.Fatalf("stats: added=%d bytes=%d, хочу %d/%d",
			res.Stats.Added, res.Stats.Bytes, wantEntries, wantBytes)
	}

	// eject-simulated: всё закрыто и открыто заново
	h.reopen()

	info, err := h.catalogUC().TapeInfo(ctx)
	if err != nil {
		t.Fatalf("tape info после переоткрытия: %v", err)
	}
	if info.Label.UUID != label.UUID || info.Label.Name != "LTO-001" {
		t.Fatalf("ярлык после переустановки: %+v, хочу uuid=%s", info.Label, label.UUID)
	}

	// каталог пережил переоткрытие
	sessions, err := h.catalogUC().ListSessions(ctx, label.UUID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != res.Session.ID {
		t.Fatalf("сессии в каталоге: %+v", sessions)
	}

	// readtest: вся лента читается, хеши сходятся, на ФС не пишется
	checked, err := h.catalogUC().ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest: %v", err)
	}
	if checked != 1 {
		t.Fatalf("readtest: проверено сессий %d, хочу 1", checked)
	}

	// restore full в отдельный каталог
	st, err := h.restoreUC().Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	if st.Sessions != 1 || st.Files != len(wantFiles) || st.Dirs != len(wantDirs)+1 {
		t.Fatalf("restore stats: %+v, хочу sessions=1 files=%d dirs=%d",
			st, len(wantFiles), len(wantDirs)+1)
	}
	assertTreeEqual(t, destRoot(h.dest, h.src), wantFiles, wantDirs)
}

// TestBackupRestore_AppendIncrementalAndSelective — вторая сессия
// (INC) после правок, восстановление всего и выборочное из сессии 1.
func TestBackupRestore_AppendIncrementalAndSelective(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-002", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "notes/a.txt", []byte("alpha v1\n"))
	writeFile(t, h.src, "notes/b.txt", []byte("bravo v1\n"))
	first, err := h.backupUC().Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup #1: %v", err)
	}

	// правки: a.txt изменён, c.txt добавлен, b.txt не тронут
	writeFile(t, h.src, "notes/a.txt", []byte("alpha v2 — перезаписан\n"))
	writeFile(t, h.src, "notes/c.txt", []byte("charlie v1\n"))
	second, err := h.backupUC().Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup #2: %v", err)
	}
	if second.Session.Num != 2 || second.Session.Type != domain.SessionInc {
		t.Fatalf("сессия #2: num=%d type=%s, хочу 2/INC", second.Session.Num, second.Session.Type)
	}
	// Modified >= 1: сам a.txt; каталоги с изменившимся mtime попадают
	// в сессию недетерминированно (Windows обновляет mtime каталогов
	// лениво), поэтому точное число не проверяем.
	if second.Stats.Added != 1 || second.Stats.Modified < 1 || second.Stats.Bytes != 47 {
		t.Fatalf("stats #2: %+v, хочу added=1 modified>=1 bytes=47", second.Stats)
	}

	// в сессии 2 — только изменённые пути
	files2, err := h.catalogUC().GetFiles(ctx, second.Session.ID)
	if err != nil {
		t.Fatalf("файлы сессии 2: %v", err)
	}
	states := map[string]domain.FileState{}
	for _, fm := range files2 {
		if !fm.IsDir { // каталоги с изменившимся mtime — недетерминированы
			states[path.Base(fm.Path)] = fm.State
		}
	}
	if states["a.txt"] != domain.StateModified || states["c.txt"] != domain.StateAdded || len(states) != 2 {
		t.Fatalf("сессия 2: состояния %v", states)
	}

	// поиск по подстроке находит обе копии a.txt
	copies, err := h.catalogUC().Search(ctx, "a.txt")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(copies) != 2 {
		t.Fatalf("search a.txt: копий %d, хочу 2", len(copies))
	}

	// restore full: обе сессии подряд, поверх старых версий — новые
	st, err := h.restoreUC().Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	if st.Sessions != 2 || st.Files != 4 {
		t.Fatalf("restore stats: %+v, хочу sessions=2 files=4", st)
	}
	wantFiles, wantDirs := treeOf(t, h.src)
	assertTreeEqual(t, destRoot(h.dest, h.src), wantFiles, wantDirs)

	// selective из сессии 1: запрошен только a.txt, версия — v1
	dest2 := h.dest + "-sel"
	sst, err := h.restoreUCTo(dest2).Selective(ctx, first.Session.ID,
		[]string{filepath.Join(h.src, "notes", "a.txt")})
	if err != nil {
		t.Fatalf("restore selective: %v", err)
	}
	if sst.Files != 1 || sst.Skipped != 3 {
		t.Fatalf("selective stats: %+v, хочу files=1 skipped=3 (корень, notes, b.txt)", sst)
	}
	selFiles, _ := treeOf(t, destRoot(dest2, h.src))
	if got := selFiles["notes/a.txt"]; string(got) != "alpha v1\n" {
		t.Fatalf("selective a.txt: %q, хочу версию из сессии 1", got)
	}
}
