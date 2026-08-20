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

// Тесты верификации после записи (сессия 13): обратное чтение каждой
// части со сверкой, политика VerifyError без откката, пограничные
// случаи spanning (позиционирование FSF, ENOSPC-перенос).

package backup_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
)

// verifyFilesN — n фиктивных FileMeta для очереди FakeCodec.ReadSession
// (верификации достаточно счётной сверки: хеши внутри проверяет кодек).
func verifyFilesN(n int) []domain.FileMeta {
	out := make([]domain.FileMeta, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.FileMeta{
			Path: fmt.Sprintf("/f/%d", i), State: domain.StateAdded, Size: 1,
		})
	}
	return out
}

// hasPhase — была ли фаза прогресса.
func hasPhase(h *harness, phase string) bool {
	for _, u := range h.prog.updates {
		if u.Phase == phase {
			return true
		}
	}
	return false
}

// TestBackup_VerifyReadsSessionBack — одиночная сессия: ReadSession
// зовётся после записи (позиционирование Rewind + MTFSF(2K−1)),
// прогресс публикует PhaseVerify, итог несёт счётчики.
func TestBackup_VerifyReadsSessionBack(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	h.codec.ReadFiles = verifyFilesN(2) // /etc + /etc/hosts
	h.rec.fsf = nil

	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
	if err != nil {
		t.Fatalf("Backup(Verify): %v", err)
	}
	if !res.Verified {
		t.Error("Verified = false; want true")
	}
	if res.VerifiedFiles != 2 {
		t.Errorf("VerifiedFiles = %d; want 2", res.VerifiedFiles)
	}
	if res.VerifiedBytes != 2 {
		t.Errorf("VerifiedBytes = %d; want 2", res.VerifiedBytes)
	}
	if h.codec.ReadCalls != 1 {
		t.Errorf("ReadSession вызовов %d; want 1", h.codec.ReadCalls)
	}
	// позиционирование: запись FSF(1), верификация FSF(2*1-1)=FSF(1)
	if len(h.rec.fsf) != 2 || h.rec.fsf[0] != 1 || h.rec.fsf[1] != 1 {
		t.Errorf("ForwardFilemarks args = %v; want [1 1] (запись + верификация)", h.rec.fsf)
	}
	if !hasPhase(h, port.PhaseVerify) {
		t.Error("прогресс без фазы verify")
	}
	if h.prog.done != 1 || h.prog.fails != 0 {
		t.Errorf("прогресс: done=%d fails=%d; want 1/0", h.prog.done, h.prog.fails)
	}
}

// TestBackup_VerifyOffByDefault — без Options.Verify и в DryRun
// обратного чтения нет.
func TestBackup_VerifyOffByDefault(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if h.codec.ReadCalls != 0 {
		t.Errorf("ReadSession вызовов %d; want 0 без Verify", h.codec.ReadCalls)
	}
	h.codec.ReadCalls = 0
	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{DryRun: true, Verify: true})
	if err != nil {
		t.Fatalf("Backup(DryRun+Verify): %v", err)
	}
	if h.codec.ReadCalls != 0 {
		t.Errorf("DryRun звал ReadSession: %d вызовов", h.codec.ReadCalls)
	}
	if res.Verified {
		t.Error("DryRun: Verified = true; want false")
	}
}

// TestBackup_VerifyErrorKeepsSession — порча при обратном чтении:
// VerifyError, сессия в каталоге остаётся, прогресс завершён ошибкой.
func TestBackup_VerifyErrorKeepsSession(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	h.codec.ErrReadOnce = errors.New("хеш не сошёлся")
	h.codec.ErrReadOn = 1

	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
	var ve *domain.VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("Backup: %v; want VerifyError", err)
	}
	if ve.SessionNum != 1 {
		t.Errorf("SessionNum = %d; want 1", ve.SessionNum)
	}
	if !strings.Contains(ve.Error(), "не читается обратно") {
		t.Errorf("текст ошибки: %q", ve.Error())
	}
	if res.Verified {
		t.Error("Verified = true при сбое верификации")
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 1 {
		t.Fatalf("сессий в каталоге %d; want 1 (отката нет)", len(sessions))
	}
	if h.prog.fails != 1 || h.prog.done != 0 {
		t.Errorf("прогресс: fails=%d done=%d; want 1/0", h.prog.fails, h.prog.done)
	}
}

// TestBackup_VerifyCountMismatch — счётная сверка состава: прочитано
// больше записанного (File пуст — прочитанное надмножество) и меньше
// с потерянным путём (File указывает первый отсутствующий).
func TestBackup_VerifyCountMismatch(t *testing.T) {
	cases := []struct {
		name       string
		read       []domain.FileMeta
		wantFile   string
		wantDetail string
	}{
		{
			name: "extra file read back",
			read: []domain.FileMeta{
				{Path: "/etc", State: domain.StateAdded},
				{Path: "/etc/hosts", State: domain.StateAdded},
				{Path: "/extra", State: domain.StateAdded},
			},
			wantFile:   "",
			wantDetail: "записано 2 файлов, прочитано 3",
		},
		{
			name: "file missing",
			read: []domain.FileMeta{{Path: "/x", State: domain.StateAdded}},
			// порядок сканера: /etc, /etc/hosts — первым отсутствует /etc
			wantFile:   "/etc",
			wantDetail: "записано 2 файлов, прочитано 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			h.codec.ReadFiles = tc.read
			_, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
			var ve *domain.VerifyError
			if !errors.As(err, &ve) {
				t.Fatalf("Backup: %v; want VerifyError", err)
			}
			if ve.File != tc.wantFile {
				t.Errorf("File = %q; want %q", ve.File, tc.wantFile)
			}
			if !strings.Contains(ve.Details, tc.wantDetail) {
				t.Errorf("Details = %q; хочу %q", ve.Details, tc.wantDetail)
			}
		})
	}
}

// TestBackup_VerifyPositioningFailures — сбои перемотки и
// позиционирования верификации — тоже VerifyError (лента не читается
// обратно), сессия остаётся в каталоге.
func TestBackup_VerifyPositioningFailures(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		prep func(t *testing.T, h *harness)
		want string
	}{
		{
			name: "rewind fails",
			prep: func(t *testing.T, h *harness) {
				// 1-я перемотка — readLabel, 2-я — writePart, 3-я — verify
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, rewindFail: 3, rewindErr: boom}
				})
			},
			want: "перемотка",
		},
		{
			name: "positioning fails",
			prep: func(t *testing.T, h *harness) {
				// 1-й FSF — запись, 2-й — верификация
				h.swapTape(func(inner *testutil.FakeTape) port.Tape {
					return &failTapeBy{FakeTape: inner, fsfFail: 2, fsfErr: boom}
				})
			},
			want: "позиционирование MTFSF(1)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"etc/hosts": "x"})
			h.codec.ReadFiles = verifyFilesN(2)
			tc.prep(t, h)
			h.rebuild()
			_, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
			var ve *domain.VerifyError
			if !errors.As(err, &ve) {
				t.Fatalf("Backup: %v; want VerifyError+boom", err)
			}
			if !strings.Contains(ve.Details, tc.want) || !strings.Contains(ve.Details, "boom") {
				t.Errorf("Details = %q; хочу причину %q c boom", ve.Details, tc.want)
			}
			sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
			if len(sessions) != 1 {
				t.Errorf("сессий %d; want 1 (отката нет)", len(sessions))
			}
		})
	}
}

// verifyFarm — фабрика кассет с журналом FSF каждой выданной ленты:
// сверка позиций записи и верификации по кассетам цепочки.
type verifyFarm struct {
	tapes []*recTape
	codec *testutil.FakeCodec
	cat   *testutil.MemCatalog
}

func (f *verifyFarm) request(
	ctx context.Context,
	req port.NextTapeRequest,
) (port.Tape, domain.TapeLabel, error) {
	tp := &recTape{FakeTape: testutil.NewFakeTape()}
	label := domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: req.NextTapeName, UUID: req.NextTapeName + "-uuid",
	}
	block, err := f.codec.EncodeLabel(label)
	if err != nil {
		return nil, domain.TapeLabel{}, err
	}
	if err := tp.WriteBlock(ctx, block); err != nil {
		return nil, domain.TapeLabel{}, err
	}
	for i := 0; i < 2; i++ {
		if err := tp.WriteEOF(ctx); err != nil {
			return nil, domain.TapeLabel{}, err
		}
	}
	if err := f.cat.RegisterTape(ctx, label.UUID, label.Name, 0); err != nil {
		return nil, domain.TapeLabel{}, err
	}
	f.tapes = append(f.tapes, tp)
	return tp, label, nil
}

// verifySpanHarness — harness с журналирующей фабрикой кассет.
func verifySpanHarness(t *testing.T, files map[string]string, capacity int64) (*harness, *verifyFarm, *testutil.FuncChanger) {
	t.Helper()
	h := newHarness(t, files)
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: capacity}
	farm := &verifyFarm{codec: h.codec, cat: h.cat}
	ch := &testutil.FuncChanger{Request: farm.request}
	h.changer = ch
	h.rebuild()
	return h, farm, ch
}

// TestBackup_VerifySpanBothPartsBeforeRotate — spanning: каждая часть
// верифицируется на своей кассете до плановой смены (по журналу
// вызовов кодека на момент запроса кассеты); указатель продолжения не
// ломает счёт FSF — на обеих кассетах [запись FSF(1), верификация
// FSF(2*1-1)].
func TestBackup_VerifySpanBothPartsBeforeRotate(t *testing.T) {
	h, farm, ch := verifySpanHarness(t, twoParts(), 100)
	h.codec.Queue = [][]domain.FileMeta{verifyFilesN(2), verifyFilesN(1)}
	var readsAtRequest []int
	base := ch.Request
	ch.Request = func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		readsAtRequest = append(readsAtRequest, h.codec.ReadCalls)
		return base(ctx, req)
	}

	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
	if err != nil {
		t.Fatalf("Backup(Verify): %v", err)
	}
	if res.Parts != 2 || !res.Verified {
		t.Fatalf("итог: частей %d verified=%v; want 2/true", res.Parts, res.Verified)
	}
	if res.VerifiedFiles != 3 {
		t.Errorf("VerifiedFiles = %d; want 3 (2 + 1)", res.VerifiedFiles)
	}
	if h.codec.ReadCalls != 2 {
		t.Errorf("ReadSession вызовов %d; want 2 (по одной на часть)", h.codec.ReadCalls)
	}
	// смена кассеты запросила после верификации части 1
	if len(readsAtRequest) != 1 || readsAtRequest[0] != 1 {
		t.Errorf("ReadCalls на момент смены = %v; want [1]", readsAtRequest)
	}
	// FSF-счёт части K: запись FSF(2K+1) c lastNum=0 → FSF(1);
	// верификация FSF(2K−1)=FSF(1); continuation-блок меток не добавляет
	if len(h.rec.fsf) != 2 || h.rec.fsf[0] != 1 || h.rec.fsf[1] != 1 {
		t.Errorf("FSF кассеты 1 = %v; want [1 1]", h.rec.fsf)
	}
	if len(farm.tapes) != 1 || len(farm.tapes[0].fsf) != 2 ||
		farm.tapes[0].fsf[0] != 1 || farm.tapes[0].fsf[1] != 1 {
		t.Errorf("FSF кассеты 2 = %v; want [1 1]", farm.tapes[0].fsf)
	}
}

// TestBackup_VerifyErrorAbortsSpan — сбой верификации части 1
// останавливает цепочку: часть 2 не пишется, смены кассет нет,
// сессия части 1 остаётся в каталоге.
func TestBackup_VerifyErrorAbortsSpan(t *testing.T) {
	h, _, ch := verifySpanHarness(t, twoParts(), 100)
	h.codec.Queue = [][]domain.FileMeta{verifyFilesN(1)} // записано 2 — не сойдётся

	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
	var ve *domain.VerifyError
	if !errors.As(err, &ve) || ve.SessionNum != 1 {
		t.Fatalf("Backup: %v; want VerifyError сессии 1", err)
	}
	if len(h.codec.WroteHeaders) != 1 {
		t.Errorf("WriteSession вызовов %d; want 1 (часть 2 не писалась)", len(h.codec.WroteHeaders))
	}
	if len(ch.Requests) != 0 {
		t.Errorf("changer запросов %d; want 0", len(ch.Requests))
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 1 {
		t.Errorf("сессий на кассете 1: %d; want 1 (отката нет)", len(sessions))
	}
}

// TestBackup_VerifyAfterENOSPCMove — ENOSPC-перенос части 2: обе
// части верифицируются на своих кассетах до смен (часть 2 — на
// кассете переноса t-3).
func TestBackup_VerifyAfterENOSPCMove(t *testing.T) {
	h, _, ch := verifySpanHarness(t, twoParts(), 100)
	h.codec.Queue = [][]domain.FileMeta{verifyFilesN(2), verifyFilesN(1)}
	// часть 2 не влезает в кассету-2: перенос на t-3
	h.codec.ErrWriteOnce = &domain.TapeFullError{}
	h.codec.ErrWriteOn = 2
	h.rand = testutil.FixedRand("run-1", "run-2", "run-3")
	h.rebuild()

	res, err := h.uc.Backup(context.Background(), "daily", backup.Options{Verify: true})
	if err != nil {
		t.Fatalf("Backup(Verify): %v", err)
	}
	if res.Parts != 2 || !res.Verified || res.VerifiedFiles != 3 {
		t.Fatalf("итог: %+v", res)
	}
	if h.codec.ReadCalls != 2 {
		t.Errorf("ReadSession вызовов %d; want 2", h.codec.ReadCalls)
	}
	if len(ch.Requests) != 2 ||
		ch.Requests[0].Reason != port.ReasonSpan || ch.Requests[1].Reason != port.ReasonEnospc {
		t.Fatalf("запросы смены: %+v; want span + enospc", ch.Requests)
	}
	// каталог: обе части живы (ENOSPC-откат неудавшейся попытки — часть 2
	// на кассете 2 — уже проверен сессией 5 плана spanning)
	s1, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	s3, _ := h.cat.ListSessions(context.Background(), "t-3-uuid")
	if len(s1) != 1 || len(s3) != 1 {
		t.Fatalf("сессий: кассета1 %d, кассета3 %d; want 1/1", len(s1), len(s3))
	}
}
