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

// Сценарии верификации после записи (verify-after-write, сессия 13):
// сквозной round-trip на реальных адаптерах и «флакающий носитель» —
// байт tar-данных портится сразу после записи сессии, верификация
// ловит несовпадение хеша, сессия остаётся в каталоге.

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
)

const (
	verifyV1 = "verify report v1 — альфа-перезапись "
	verifyV2 = "verify report v2 — бета-перезапись "
)

// verifyHarness — лента с заданием daily и первой сессией report.txt (v1).
func verifyHarness(t *testing.T) (*harness, domain.TapeLabel) {
	t.Helper()
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	ctx := context.Background()
	label, err := h.formatUC().Format(ctx, "LTO-V01", false)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "report.txt", []byte(strings.Repeat(verifyV1, 20)))
	writeFile(t, h.src, "notes/other.txt", []byte("other v1\n"))
	return h, label
}

// TestBackupVerify_RoundTrip — бекап с верификацией на реальных
// адаптерах: позиционирование FSF(2K−1), обратное чтение со сверкой
// хешей, счётчики в итоге; лента после верификации консистентна
// (readtest проходит).
func TestBackupVerify_RoundTrip(t *testing.T) {
	h, _ := verifyHarness(t)
	ctx := context.Background()

	res, err := h.backupUC().Backup(ctx, "daily", backup.Options{Verify: true})
	if err != nil {
		t.Fatalf("backup(Verify): %v", err)
	}
	if !res.Verified {
		t.Fatal("Verified = false")
	}
	// /src + report.txt + notes + notes/other.txt — 4 записи сессии
	if res.VerifiedFiles != 4 {
		t.Errorf("VerifiedFiles = %d; want 4", res.VerifiedFiles)
	}
	if res.VerifiedBytes != int64(len(strings.Repeat(verifyV1, 20))+len("other v1\n")) {
		t.Errorf("VerifiedBytes = %d", res.VerifiedBytes)
	}

	// лента консистентна: readtest после переоткрытия чист
	h.reopen()
	reports, err := h.catalogUC().ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest после verify: %v", err)
	}
	if len(reports) != 1 || reports[0].Sessions != 1 {
		t.Fatalf("readtest: %+v, хочу 1 кассету с 1 сессией", reports)
	}
}

// flakyMediaCodec — реальный кодек на «флакающем» носителе: после
// writeOn-й записанной сессии corruptNeedle портит один байт её данных
// в файле-ленте (framing не трогается — носитель записал мусор; хеш в
// индексе считается по источнику и сойтись не может). Порча делается
// между записью и верификацией той же сессии.
type flakyMediaCodec struct {
	*tapeformat.Codec
	t        *testing.T
	tapePath string
	needle   string
	writeOn  int
	writes   int
}

// WriteSession делегирует реальному кодеку, затем портит байт-иглу.
func (c *flakyMediaCodec) WriteSession(
	ctx context.Context,
	tape port.Tape,
	header port.SessionHeader,
	files []domain.FileMeta,
	fs port.FileReader,
	prog port.ProgressReporter,
) error {
	err := c.Codec.WriteSession(ctx, tape, header, files, fs, prog)
	if err == nil {
		c.writes++
		if c.writes == c.writeOn {
			corruptNeedle(c.t, c.tapePath, c.needle)
		}
	}
	return err
}

// TestBackupVerify_CorruptedWriteKeepsCatalog — носитель записал мусор:
// бекап с --verify возвращает VerifyError, сессия НЕ откатывается —
// данные уже на ленте и в каталоге, каталог жив.
func TestBackupVerify_CorruptedWriteKeepsCatalog(t *testing.T) {
	h, label := verifyHarness(t)
	ctx := context.Background()

	// первый бекап с верификацией — чистый round-trip
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{Verify: true}); err != nil {
		t.Fatalf("backup #1(Verify): %v", err)
	}

	// меняем report.txt: сессия 2 понесёт v2 — её данные и портим
	writeFile(t, h.src, "report.txt", []byte(strings.Repeat(verifyV2, 20)))
	flaky := &flakyMediaCodec{
		Codec: h.codec, t: t, tapePath: h.tapePath,
		needle: verifyV2, writeOn: 1,
	}
	uc := backup.New(h.cfg, h.tape, flaky, h.cat, h.fs, h.hasher,
		h.rnd, h.clock, nil, testutil.NoopLogger(), nil)

	_, err := uc.Backup(ctx, "daily", backup.Options{Verify: true})
	var ve *domain.VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("backup #2(Verify): err=%v; want VerifyError", err)
	}
	if ve.SessionNum != 2 {
		t.Errorf("SessionNum = %d; want 2", ve.SessionNum)
	}
	if !strings.Contains(ve.Error(), "не читается обратно") || !strings.Contains(ve.Error(), "report.txt") {
		t.Errorf("текст ошибки оператору: %q", ve.Error())
	}

	// каталог жив: обе сессии на месте (сессия 2 не откачена),
	// файлы доступны
	h.reopen()
	sessions, err := h.cat.ListSessions(ctx, label.UUID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("сессий в каталоге %d; want 2 (отката нет)", len(sessions))
	}
	files1, err := h.cat.GetFilesBySession(ctx, sessions[0].ID)
	if err != nil {
		t.Fatalf("GetFilesBySession(%d): %v", sessions[0].ID, err)
	}
	var hasReport bool
	for _, fm := range files1 {
		if strings.HasSuffix(fm.Path, "report.txt") {
			hasReport = true
		}
	}
	if !hasReport {
		t.Errorf("файлы сессии 1: %v — нет report.txt", files1)
	}
	files2, err := h.cat.GetFilesBySession(ctx, sessions[1].ID)
	if err != nil || len(files2) == 0 {
		t.Fatalf("файлы сессии 2: %v (%v) — каталог должен жить", files2, err)
	}
}
