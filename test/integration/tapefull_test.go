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

// Сценарий «кончилась лента» на filetape с лимитом ёмкости (сессия 1
// плана spanning, логи/тома.md §2.3): бекап упирается в лимит посреди
// tar → TapeFullError + откат ленты к старому EOD; readtest подтверждает
// отсутствие грязного хвоста; повторный бекап меньшего объёма
// дозаписывается и читается restore-full.

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

// TestTapeFull_RollbackAndRetry — ENOSPC-откат и восстановление:
//   - format на ленте с лимитом 20*BlockSize+8 (ярлык + индекс + 18
//     блоков tar + метки); файл 10 МиБ требует ~41 блок — сбой посреди
//     tar, после успешно записанных блоков;
//   - TapeFullError с Written > 0, сессия откачена из каталога;
//   - readtest после сбоя: 0 сессий, ошибок нет (EOD-хвост чист);
//   - меньший бекап дозаписывается (Num=1 FULL), переживает reopen;
//   - restore-full восстанавливает дерево побайтово.
func TestTapeFull_RollbackAndRetry(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	ctx := context.Background()

	// Лимит: блок ярлыка + блок индекса + 18 блоков tar + filemark'и
	// с запасом; первый 4-МиБ чанк tar (16 блоков) влезает целиком,
	// второй упирается в лимит — сбой посреди tar-потока.
	h.tapeCapacity = int64(20*domain.BlockSize + 8)
	h.reopen()

	label, err := h.formatUC().Format(ctx, "LTO-FULL", false)
	if err != nil {
		t.Fatalf("format: %v", err)
	}

	// Большой бекап упирается в лимит ленты посреди tar-потока.
	big := make([]byte, 10*1024*1024)
	for i := range big {
		big[i] = byte(i % 251)
	}
	writeFile(t, h.src, "big.bin", big)
	_, err = h.backupUC().Backup(ctx, "daily", backup.Options{})
	var full *domain.TapeFullError
	if !errors.As(err, &full) {
		t.Fatalf("backup: %v; want TapeFullError", err)
	}
	if full.Written <= 0 {
		t.Errorf("TapeFullError.Written = %d; want > 0 (запись уже началась)", full.Written)
	}

	// Сессия откачена из каталога; лента без грязного хвоста — readtest
	// проходит и не находит сессий.
	sessions, err := h.catalogUC().ListSessions(ctx, label.UUID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("сессий в каталоге после сбоя %d; want 0", len(sessions))
	}
	checked, err := h.catalogUC().ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest после сбоя: %v (грязный хвост не откачен?)", err)
	}
	if checked != 0 {
		t.Fatalf("readtest после сбоя: сессий %d; want 0", checked)
	}

	// Меньший бекап дозаписывается на восстановленную ленту.
	if err := os.Remove(filepath.Join(h.src, "big.bin")); err != nil {
		t.Fatalf("удаление big.bin: %v", err)
	}
	writeFile(t, h.src, "small/notes.txt", []byte("маленький файл после ENOSPC\n"))
	res, err := h.backupUC().Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup после сбоя: %v", err)
	}
	if res.Session.Num != 1 || res.Session.Type != domain.SessionFull {
		t.Fatalf("сессия: num=%d type=%s; want 1/FULL", res.Session.Num, res.Session.Type)
	}

	// Дозапись пережила закрытие/переоткрытие ленты и каталога.
	h.reopen()
	checked, err = h.catalogUC().ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest после дозаписи: %v", err)
	}
	if checked != 1 {
		t.Fatalf("readtest после дозаписи: сессий %d; want 1", checked)
	}

	// Restore full восстанавливает всё дерево побайтово.
	wantFiles, wantDirs := treeOf(t, h.src)
	st, err := h.restoreUC().Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	if st.Sessions != 1 || st.Files != len(wantFiles) || st.Dirs != len(wantDirs)+1 {
		t.Fatalf("restore stats: %+v; want sessions=1 files=%d dirs=%d",
			st, len(wantFiles), len(wantDirs)+1)
	}
	assertTreeEqual(t, destRoot(h.dest, h.src), wantFiles, wantDirs)
}
