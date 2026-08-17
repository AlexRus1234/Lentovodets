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

// Тесты цикла spanning: части на разные кассеты, указатели
// продолжения, ENOSPC-перенос, min_tail, отмена на смене кассеты.

package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
)

// tapeFarm — авто-фабрика кассет для FuncChanger: каждая следующая —
// свежий FakeTape с ярлыком и регистрацией в каталоге.
type tapeFarm struct {
	tapes []*testutil.FakeTape
	codec *testutil.FakeCodec
	cat   *testutil.MemCatalog
}

// request выдаёт новую отформатированную кассету с именем req.NextTapeName.
func (f *tapeFarm) request(
	ctx context.Context,
	req port.NextTapeRequest,
) (port.Tape, domain.TapeLabel, error) {
	tp := testutil.NewFakeTape()
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

// spanHarness — harness с фабрикой кассет и capacity в конфиге.
func spanHarness(t *testing.T, files map[string]string, capacity int64) (*harness, *tapeFarm, *testutil.FuncChanger) {
	t.Helper()
	h := newHarness(t, files)
	h.cfg = &fakeConfig{jobs: jobsAppend(), capacity: capacity}
	farm := &tapeFarm{codec: h.codec, cat: h.cat}
	ch := &testutil.FuncChanger{Request: farm.request}
	h.changer = ch
	h.rebuild()
	return h, farm, ch
}

// twoParts — файлы 60 + 50 байт: при бюджете 100 режутся на 2 части.
func twoParts() map[string]string {
	return map[string]string{
		"etc/a": strings.Repeat("a", 60),
		"etc/b": strings.Repeat("b", 50),
	}
}

// TestBackup_SpanTwoTapes — плановый spanning: две части на двух
// кассетах, указатель продолжения после части 1, каталог — цепочка
// с общим JobRunID; на второй кассете сессия 1 FULL.
func TestBackup_SpanTwoTapes(t *testing.T) {
	h, farm, ch := spanHarness(t, twoParts(), 100)
	ctx := context.Background()
	res, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if res.Parts != 2 || res.PlannedParts != 2 {
		t.Errorf("частей: %d/%d; want 2/2", res.Parts, res.PlannedParts)
	}
	if res.PlannedByBudget {
		t.Error("PlannedByBudget = true; want false (первая сессия — бюджет capacity, не остаток)")
	}
	if got := strings.Join(res.Tapes, ","); got != "t-1,t-2" {
		t.Errorf("Tapes = %q; want t-1,t-2", got)
	}

	// заголовки: часть 1 без Continues, часть 2 — с UUID кассеты 1
	if len(h.codec.WroteHeaders) != 2 {
		t.Fatalf("WriteSession вызовов %d; want 2", len(h.codec.WroteHeaders))
	}
	h1, h2 := h.codec.WroteHeaders[0], h.codec.WroteHeaders[1]
	if h1.Part != 1 || h1.Continues != "" || h1.SessionNum != 1 || h1.Type != domain.SessionFull {
		t.Errorf("заголовок части 1: %+v", h1)
	}
	if h2.Part != 2 || h2.Continues != "tape-uuid" || h2.SessionNum != 1 || h2.Type != domain.SessionFull {
		t.Errorf("заголовок части 2: %+v", h2)
	}
	if h1.JobRunID != h2.JobRunID || h1.JobRunID != "run-uuid" {
		t.Errorf("JobRunID частей: %q vs %q; want run-uuid у обеих", h1.JobRunID, h2.JobRunID)
	}

	// указатель продолжения: один, после части 1, ведёт на t-2
	if len(h.codec.WroteConts) != 1 {
		t.Fatalf("WriteContinuation вызовов %d; want 1", len(h.codec.WroteConts))
	}
	if cont := h.codec.WroteConts[0]; cont.JobRunID != "run-uuid" ||
		cont.SessionNum != 1 || cont.Part != 2 || cont.NextTapeName != "t-2" {
		t.Errorf("указатель продолжения: %+v", cont)
	}

	// смена кассеты: один запрос, плановый, после части 1
	if len(ch.Requests) != 1 || ch.Closed != 1 {
		t.Fatalf("changer: запросов %d, закрытий %d; want 1/1", len(ch.Requests), ch.Closed)
	}
	if req := ch.Requests[0]; req.Reason != port.ReasonSpan || req.Part != 2 ||
		req.FinishedTape != "t-1" || req.NextTapeName != "t-2" || req.JobName != "daily" {
		t.Errorf("запрос смены: %+v", req)
	}

	// каталог: часть 1 на кассете 1, часть 2 (Num=1 FULL) на кассете 2
	s1, _ := h.cat.ListSessions(ctx, "tape-uuid")
	s2, _ := h.cat.ListSessions(ctx, "t-2-uuid")
	if len(s1) != 1 || len(s2) != 1 {
		t.Fatalf("сессий: кассета1 %d, кассета2 %d; want 1/1", len(s1), len(s2))
	}
	if s2[0].Num != 1 || s2[0].Type != domain.SessionFull || s2[0].Part != 2 {
		t.Errorf("сессия части 2: %+v; want Num=1 FULL Part=2", s2[0])
	}
	chain, err := h.cat.GetSessionChain(ctx, "run-uuid")
	if err != nil {
		t.Fatalf("GetSessionChain: %v", err)
	}
	if len(chain) != 2 || chain[0].Part != 1 || chain[1].Part != 2 {
		t.Fatalf("цепочка: %+v; want части 1,2", chain)
	}
	files1, _ := h.cat.GetFilesBySession(ctx, s1[0].ID)
	files2, _ := h.cat.GetFilesBySession(ctx, s2[0].ID)
	if len(files1) != 2 || len(files2) != 1 { // /etc + a; b
		t.Errorf("файлы частей: %d и %d; want 2 и 1", len(files1), len(files2))
	}

	// геометрия лент: инвариант 2K+3 меток (K=1) на обеих
	if got := h.fakeTape.MarkCount(); got != 3 {
		t.Errorf("меток на кассете 1: %d; want 3", got)
	}
	if len(farm.tapes) != 1 || farm.tapes[0].MarkCount() != 3 {
		t.Errorf("меток на кассете 2: %d; want 3", farm.tapes[0].MarkCount())
	}

	// прогресс: сообщение о смене кассеты оператору
	var tapeChange bool
	for _, u := range h.prog.updates {
		if u.Phase == port.PhaseTapeChange && strings.Contains(u.Message, "t-2") {
			tapeChange = true
		}
	}
	if !tapeChange {
		t.Error("нет прогресса смены кассеты с именем t-2")
	}
	if h.prog.done != 1 || h.prog.fails != 0 {
		t.Errorf("прогресс: done=%d fails=%d; want 1/0", h.prog.done, h.prog.fails)
	}
}

// TestBackup_ENOSPCMovesPartToNextTape — TapeFullError посреди части:
// откат ленты-1 к старому EOD (сессия 1 плана), указатель продолжения
// дописан, часть целиком на новой кассете, данные в каталоге.
func TestBackup_ENOSPCMovesPartToNextTape(t *testing.T) {
	h, farm, ch := spanHarness(t, map[string]string{"etc/a": strings.Repeat("a", 60)}, 1000)
	h.codec.ErrWriteOnce = &domain.TapeFullError{}
	h.codec.ErrWriteOn = 1
	ctx := context.Background()
	res, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if res.Parts != 1 || res.PlannedParts != 1 {
		t.Errorf("частей: %d/%d; want 1/1 (перенос не меняет число частей)", res.Parts, res.PlannedParts)
	}
	if got := strings.Join(res.Tapes, ","); got != "t-2" {
		t.Errorf("Tapes = %q; want t-2 (данные только на новой кассете)", got)
	}
	if len(ch.Requests) != 1 || ch.Requests[0].Reason != port.ReasonEnospc || ch.Requests[0].Part != 1 {
		t.Fatalf("запрос смены: %+v; want enospc-перенос части 1", ch.Requests)
	}
	// указатель на заполненной кассете-1: сессия 1, часть 1 — на t-2
	if len(h.codec.WroteConts) != 1 {
		t.Fatalf("WriteContinuation вызовов %d; want 1", len(h.codec.WroteConts))
	}
	if cont := h.codec.WroteConts[0]; cont.SessionNum != 1 || cont.Part != 1 || cont.NextTapeName != "t-2" {
		t.Errorf("указатель продолжения: %+v", cont)
	}
	// лента-1 чиста: только ярлык и восстановленный EOD (K=0 → 3 метки)
	if got := h.fakeTape.MarkCount(); got != 3 {
		t.Errorf("меток на кассете 1: %d; want 3 (грязного хвоста нет)", got)
	}
	// каталог: сессия части 1 на кассете 2
	s1, _ := h.cat.ListSessions(ctx, "tape-uuid")
	s2, _ := h.cat.ListSessions(ctx, "t-2-uuid")
	if len(s1) != 0 || len(s2) != 1 {
		t.Fatalf("сессий: кассета1 %d, кассета2 %d; want 0/1", len(s1), len(s2))
	}
	if s2[0].Num != 1 || s2[0].Type != domain.SessionFull || s2[0].Part != 1 {
		t.Errorf("сессия части 1: %+v; want Num=1 FULL Part=1", s2[0])
	}
	if len(farm.tapes) != 1 {
		t.Fatalf("кассет выдано %d; want 1", len(farm.tapes))
	}
}

// TestBackup_MinTailStartsNewTape — остаток ниже min_tail: часть 1
// сразу на новую кассету через changer, старая закрыта как есть.
func TestBackup_MinTailStartsNewTape(t *testing.T) {
	h, _, ch := spanHarness(t, map[string]string{"etc/hosts": strings.Repeat("x", 100)}, 1000)
	h.cfg.minTail = 950
	h.rand = testutil.FixedRand("run-1", "run-2")
	h.rebuild()
	ctx := context.Background()
	if _, err := h.uc.Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup#1: %v", err)
	}
	if len(ch.Requests) != 0 {
		t.Fatalf("Backup#1 сменил кассету: %+v", ch.Requests)
	}
	h.fs.MapFS["etc/new"] = &fstest.MapFile{Data: []byte(strings.Repeat("n", 50)), Mode: 0o644}
	res, err := h.uc.Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("Backup#2: %v", err)
	}
	if len(ch.Requests) != 1 || ch.Requests[0].Reason != port.ReasonSpan || ch.Requests[0].Part != 1 {
		t.Fatalf("запрос смены: %+v; want плановая смена перед частью 1", ch.Requests)
	}
	if got := strings.Join(res.Tapes, ","); got != "t-2" {
		t.Errorf("Tapes = %q; want t-2", got)
	}
	if res.Session.TapeUUID != "t-2-uuid" || res.Session.Num != 1 || res.Session.Type != domain.SessionFull {
		t.Errorf("сессия: %+v; want Num=1 FULL на t-2", res.Session)
	}
	s1, _ := h.cat.ListSessions(ctx, "tape-uuid")
	s2, _ := h.cat.ListSessions(ctx, "t-2-uuid")
	if len(s1) != 1 || len(s2) != 1 {
		t.Fatalf("сессий: кассета1 %d, кассета2 %d; want 1/1", len(s1), len(s2))
	}
}

// TestBackup_CancelWhileAwaitingNextTape — отмена контекста во время
// ожидания смены кассеты: запуск отменяется, зафиксированные сессии
// откатываются из каталога, указатель на закрытой кассете остаётся.
func TestBackup_CancelWhileAwaitingNextTape(t *testing.T) {
	h, _, ch := spanHarness(t, twoParts(), 100)
	awaiting := make(chan struct{})
	ch.Request = func(ctx context.Context, _ port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		close(awaiting)
		<-ctx.Done()
		return nil, domain.TapeLabel{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := h.uc.Backup(ctx, "daily", backup.Options{})
		errCh <- err
	}()
	select {
	case <-awaiting:
	case <-time.After(5 * time.Second):
		t.Fatal("changer не запросил кассету")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Backup: %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Backup не завершился после отмены")
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("сессии не откатаны из каталога: %+v", sessions)
	}
	if len(h.codec.WroteConts) != 1 {
		t.Errorf("WriteContinuation вызовов %d; want 1 (указатель на закрытой кассете)", len(h.codec.WroteConts))
	}
}

// TestBackup_TwoCapacityMovesFail — два подряд ENOSPC-переноса с
// бюджетом capacity: оценка ёмкости промахнулась, честная ошибка
// с рекомендацией уменьшить capacity.
func TestBackup_TwoCapacityMovesFail(t *testing.T) {
	h, farm, _ := spanHarness(t, map[string]string{"etc/a": strings.Repeat("a", 60)}, 100)
	h.codec.ErrWriteOnce = &domain.TapeFullError{}
	h.codec.ErrWriteOn = 1
	ch := h.changer.(*testutil.FuncChanger)
	ch.Request = func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		farm.codec.ErrWriteOnce = &domain.TapeFullError{}
		farm.codec.ErrWriteOn = farm.codec.WriteCalls + 1
		return farm.request(ctx, req)
	}
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if err == nil || !strings.Contains(err.Error(), "уменьшите capacity") {
		t.Fatalf("Backup: %v; want рекомендация уменьшить capacity", err)
	}
	if len(farm.tapes) != 1 {
		t.Errorf("кассет выдано %d; want 1 (второй перенос подряд запрещён до запроса кассеты)", len(farm.tapes))
	}
	sessions, _ := h.cat.ListSessions(context.Background(), "tape-uuid")
	if len(sessions) != 0 {
		t.Errorf("каталог не пуст: %+v", sessions)
	}
}

// TestBackup_AbandonFailsJoins — сбой записи указателя продолжения
// на заполненной кассете: исходная TapeFullError и сбой объединены.
func TestBackup_AbandonFailsJoins(t *testing.T) {
	h, _, _ := spanHarness(t, map[string]string{"etc/a": strings.Repeat("a", 60)}, 100)
	h.codec.ErrWriteOnce = &domain.TapeFullError{}
	h.codec.ErrWriteOn = 1
	boom := errors.New("boom")
	h.codec.ErrWriteCont = boom
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	var full *domain.TapeFullError
	if !errors.As(err, &full) || !errors.Is(err, boom) {
		t.Fatalf("Backup: %v; want joined TapeFullError+boom", err)
	}
}

// TestBackup_SuggestFailsRollsBack — сбой подбора имени следующей
// кассеты: ошибка changer'а, запуск отменён до записи части.
func TestBackup_SuggestFailsRollsBack(t *testing.T) {
	h, _, _ := spanHarness(t, twoParts(), 100)
	boom := errors.New("boom")
	h.changer.(*testutil.FuncChanger).Suggest = func(context.Context, string) (string, error) {
		return "", boom
	}
	h.rebuild()
	_, err := h.uc.Backup(context.Background(), "daily", backup.Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("Backup: %v; want boom", err)
	}
	if len(h.codec.WroteHeaders) != 0 {
		t.Errorf("лента тронута: %d вызовов WriteSession", len(h.codec.WroteHeaders))
	}
}

// TestBackup_SinglePartSkipsChanger — одна часть: changer не вызывается.
func TestBackup_SinglePartSkipsChanger(t *testing.T) {
	h, _, ch := spanHarness(t, map[string]string{"etc/a": strings.Repeat("a", 60)}, 1000)
	if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if len(ch.Requests) != 0 || ch.Closed != 0 {
		t.Errorf("changer тронут: запросов %d, закрытий %d; want 0/0", len(ch.Requests), ch.Closed)
	}
}
