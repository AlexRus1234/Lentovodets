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

// Реконструкция каталога с ленты (сессия 12 плана): бекап на filetape,
// утеря БД (свежий dr.db без записей) → rebuild из индексов →
// GetSessionChain/поиск работают как с родным каталогом; идемпотентный
// повтор; spanning-цепочка — Warn о продолжении и rebuild второй
// кассеты. Все адаптеры реальные.

package integration

import (
	"context"
	"path/filepath"
	"testing"

	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
	"lentovodec/internal/usecase/catalog"
)

// rebuiltUC — catalog.UseCase реконструкции на сторонней БД.
func rebuiltUC(cat port.Catalog, tape port.Tape, codec port.TapeCodec) *catalog.UseCase {
	return catalog.New(cat, tape, codec, nil, testutil.NoopLogger(), nil)
}

// TestRebuild_FreshDB — DR-сценарий «утерян lentovodec.db»: две сессии
// на кассете пересобираются в пустую базу из одних индексов; каталог
// эквивалентен родному; повтор — всё пропущено.
func TestRebuild_FreshDB(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	ctx := context.Background()

	label, err := h.formatUC().Format(ctx, "LTO-001", false)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "docs/readme.md", []byte("v1\n"))
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("backup #1: %v", err)
	}
	writeFile(t, h.src, "docs/readme.md", []byte("v2 — изменился\n"))
	writeFile(t, h.src, "notes.txt", []byte("новый файл\n"))
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("backup #2: %v", err)
	}

	// «утерянный» каталог: свежая БД без записей; лента — та же
	h.reopen()
	drCat, err := sqlite.New(filepath.Join(filepath.Dir(h.tapePath), "dr.db"))
	if err != nil {
		t.Fatalf("sqlite.New(dr): %v", err)
	}
	defer func() { _ = drCat.Close() }()

	rep, err := rebuiltUC(drCat, h.tape, h.codec).Rebuild(ctx)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	native, err := h.cat.ListSessions(ctx, label.UUID)
	if err != nil {
		t.Fatalf("родной каталог: %v", err)
	}
	if rep.TapeName != "LTO-001" ||
		rep.Sessions != len(native) || rep.SkippedSessions != 0 {
		t.Fatalf("отчёт: %+v; сессий на ленте %d", rep, len(native))
	}

	// кассета и сессии эквивалентны родному каталогу
	rec, err := drCat.GetTapeByUUID(ctx, label.UUID)
	if err != nil || rec.Name != "LTO-001" {
		t.Fatalf("кассета в dr: %+v %v", rec, err)
	}
	rebuilt, err := drCat.ListSessions(ctx, label.UUID)
	if err != nil {
		t.Fatalf("ListSessions(dr): %v", err)
	}
	if len(rebuilt) != len(native) {
		t.Fatalf("сессий в dr: %d; want %d", len(rebuilt), len(native))
	}
	for i := range rebuilt {
		got, want := rebuilt[i], native[i]
		got.ID, want.ID = 0, 0
		if got != want {
			t.Errorf("сессия[%d] = %+v; want %+v", i, got, want)
		}
	}

	// файлы и поиск — как с родным каталогом
	wantFiles, err := h.cat.GetFilesBySession(ctx, native[len(native)-1].ID)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	gotFiles, err := drCat.GetFilesBySession(ctx, rebuilt[len(rebuilt)-1].ID)
	if err != nil {
		t.Fatalf("GetFilesBySession(dr): %v", err)
	}
	if len(gotFiles) != len(wantFiles) {
		t.Fatalf("файлов последней сессии: %d; want %d", len(gotFiles), len(wantFiles))
	}
	if rep.Files == 0 {
		t.Fatal("в отчёте нет файлов")
	}
	copies, err := drCat.SearchFiles(ctx, "readme")
	if err != nil || len(copies) != 2 { // обе сессии содержат версию
		t.Fatalf("search(dr): %v %v; want 2 копии", copies, err)
	}

	// идемпотентный повтор: всё пропущено, дубликатов нет
	rep2, err := rebuiltUC(drCat, h.tape, h.codec).Rebuild(ctx)
	if err != nil {
		t.Fatalf("повторный rebuild: %v", err)
	}
	if rep2.Sessions != 0 || rep2.SkippedSessions != len(native) {
		t.Fatalf("повтор: %+v; хочу sessions=0 skipped=%d", rep2, len(native))
	}
	after, _ := drCat.ListSessions(ctx, label.UUID)
	if len(after) != len(native) {
		t.Fatalf("после повтора сессий %d; want %d", len(after), len(native))
	}
}

// TestRebuild_SpanningChain — кассета с указателем продолжения:
// rebuild сохраняет прочитанную часть и советует следующую кассету;
// rebuild второй кассеты собирает цепочку GetSessionChain целиком.
func TestRebuild_SpanningChain(t *testing.T) {
	h, farm, _, res, label := appendSplitSetup(t)
	ctx := context.Background()

	// часть 1 (кассета LTO-001): сессии 1 и 2 (part 1), продолжение
	h.remountTape1()
	drCat, err := sqlite.New(filepath.Join(filepath.Dir(h.tapePath), "dr-span.db"))
	if err != nil {
		t.Fatalf("sqlite.New(dr): %v", err)
	}
	defer func() { _ = drCat.Close() }()

	rep, err := rebuiltUC(drCat, h.tape, h.codec).Rebuild(ctx)
	if err != nil {
		t.Fatalf("rebuild (лента-1): %v", err)
	}
	if rep.Sessions != 2 || rep.NextTapeName != "LTO-002" {
		t.Fatalf("отчёт ленты-1: %+v; хочу 2 сессии, продолжение на LTO-002", rep)
	}

	// часть 2 (кассета LTO-002): сессия 1 части 2 с обратной ссылкой
	tape2, err := farm.openTapeFile(farm.paths["LTO-002"])
	if err != nil {
		t.Fatalf("filetape.Open(LTO-002): %v", err)
	}
	rep2, err := rebuiltUC(drCat, tape2, h.codec).Rebuild(ctx)
	if err != nil {
		t.Fatalf("rebuild (лента-2): %v", err)
	}
	if rep2.Sessions != 1 || rep2.NextTapeName != "" {
		t.Fatalf("отчёт ленты-2: %+v; хочу 1 сессию без продолжения", rep2)
	}

	// цепочка запуска собрана из двух кассет, как в родном каталоге
	chain, err := drCat.GetSessionChain(ctx, res.Session.JobRunID)
	if err != nil {
		t.Fatalf("GetSessionChain(dr): %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("цепочка в dr: %d частей; want 2", len(chain))
	}
	if chain[0].Part != 1 || chain[0].TapeUUID != label.UUID || chain[0].Num != res.Session.Num ||
		chain[1].Part != 2 || chain[1].Num != 1 || chain[1].Type != domain.SessionFull {
		t.Fatalf("цепочка: %+v", chain)
	}
}
