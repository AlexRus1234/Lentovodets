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

// Тесты следования цепочке кассет при Full-restore (сессия 6 плана
// spanning): смена кассеты через changer, сверка ярлыка и обратной
// ссылки continues, отмена контекста, selective из части 2.

package restore_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/restore"
)

// labeledChainTape — свежая лента с ярлыком name/uuid и filemark'ами
// одной записанной сессии (модель кассеты цепочки).
func labeledChainTape(t *testing.T, codec *testutil.FakeCodec, name, uuid string) *testutil.FakeTape {
	t.Helper()
	tp := testutil.NewFakeTape()
	block, err := codec.EncodeLabel(domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: name, UUID: uuid,
	})
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	ctx := context.Background()
	if err := tp.WriteBlock(ctx, block); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	for i := 0; i < 3; i++ { // filemark ярлыка + индекс и tar сессии
		if err := tp.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF: %v", err)
		}
	}
	return tp
}

// chainSetup — кассета T1 с сессией-частью 1 и указателем
// продолжения на T2 (часть 2); changer выдаёт tape2 с ярлыком.
// Возвращает harness с подключённым changer'ом и tape2.
func chainSetup(t *testing.T, registerChain bool) (*harness, *testutil.FakeTape, *testutil.FuncChanger) {
	t.Helper()
	h := newHarness(t)
	h.addSession(t, 1, []domain.FileMeta{meta("/part1/a")})
	h.codec.ContOnCall = 2
	h.codec.Cont = &domain.ContinuationError{
		JobRunID: "run", SessionNum: 2, Part: 2, NextTapeName: "T2",
	}
	h.codec.Queue = append(h.codec.Queue, []domain.FileMeta{meta("/part2/b")})
	h.codec.Headers = []port.SessionHeader{{
		SessionNum: 1, Type: domain.SessionFull, JobRunID: "run",
		Part: 2, Continues: "tape-uuid",
	}}
	if registerChain {
		ctx := context.Background()
		if err := h.cat.RegisterTape(ctx, "tape-uuid-2", "T2", 1); err != nil {
			t.Fatalf("RegisterTape: %v", err)
		}
		if _, err := h.cat.CreateSession(ctx, domain.Session{
			TapeUUID: "tape-uuid-2", Num: 1, Type: domain.SessionFull,
			Timestamp: 101, JobRunID: "run", Part: 2,
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	tape2 := labeledChainTape(t, h.codec, "T2", "tape-uuid-2")
	ch := &testutil.FuncChanger{
		Request: func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			return tape2, domain.TapeLabel{
				Magic: domain.Magic, FormatVersion: domain.FormatVersion,
				Name: "T2", UUID: "tape-uuid-2",
			}, nil
		},
	}
	h.setChanger(ch)
	return h, tape2, ch
}

// TestRestore_FullFollowsChain — DR-сценарий: две кассеты, каталог
// цепочку не знает; все сессии обеих частей прочитаны, запрос смены
// корректен, прогресс несёт сообщение оператору.
func TestRestore_FullFollowsChain(t *testing.T) {
	h, _, ch := chainSetup(t, false)

	st, err := h.uc.Full(context.Background())
	if err != nil {
		t.Fatalf("Full: %v", err)
	}
	if st.Sessions != 2 || st.Files != 2 {
		t.Fatalf("статистика: %+v; want 2 сессии, 2 файла", st)
	}
	if len(ch.Requests) != 1 {
		t.Fatalf("запросов смены %d; want 1", len(ch.Requests))
	}
	req := ch.Requests[0]
	if req.FinishedTape != "t-1" || req.NextTapeName != "T2" || req.Part != 2 || req.Reason != port.ReasonRestore {
		t.Errorf("запрос смены: %+v", req)
	}
	if h.codec.HeaderCalls != 1 {
		t.Errorf("HeaderCalls = %d; want 1 (сверка continues)", h.codec.HeaderCalls)
	}
	if h.prog.done != 1 || h.prog.fails != 0 {
		t.Errorf("прогресс: done=%d fails=%d; want 1/0", h.prog.done, h.prog.fails)
	}
	var msg bool
	for _, u := range h.prog.updates {
		if u.Phase == port.PhaseTapeChange && strings.Contains(u.Message, "T2") {
			msg = true
		}
	}
	if !msg {
		t.Errorf("нет сообщения о смене кассеты в прогрессе: %+v", h.prog.updates)
	}
}

// TestRestore_FullChainBestEffortReport — каталог знает запуск:
// в лог попадает отчёт о длине цепочки (best-effort, решения —
// только по лентам).
func TestRestore_FullChainBestEffortReport(t *testing.T) {
	h, _, ch := chainSetup(t, true)
	var buf bytes.Buffer
	h.uc = restore.New(h.tape, h.codec, h.cat, h.dest, nil,
		slog.New(slog.NewTextHandler(&buf, nil)), ch)

	if _, err := h.uc.Full(context.Background()); err != nil {
		t.Fatalf("Full: %v", err)
	}
	if !strings.Contains(buf.String(), "parts_known=2") {
		t.Errorf("лог без отчёта о цепочке: %q", buf.String())
	}
}

// chainFailCat — каталог, чей GetSessionChain всегда падает (сбой
// best-effort отчёта; решения принимаются только по лентам).
type chainFailCat struct {
	*testutil.MemCatalog
	err error
}

func (c *chainFailCat) GetSessionChain(context.Context, string) ([]domain.Session, error) {
	return nil, c.err
}

// TestRestore_FullChainCatalogReportFails — сбой каталога при отчёте
// не роняет восстановление: Warn и продолжение.
func TestRestore_FullChainCatalogReportFails(t *testing.T) {
	h, _, ch := chainSetup(t, true)
	boom := errors.New("catalog down")
	fc := &chainFailCat{MemCatalog: h.cat, err: boom}
	var buf bytes.Buffer
	h.uc = restore.New(h.tape, h.codec, fc, h.dest, nil,
		slog.New(slog.NewTextHandler(&buf, nil)), ch)

	st, err := h.uc.Full(context.Background())
	if err != nil {
		t.Fatalf("Full: %v; сбой best-effort отчёта не фатален", err)
	}
	if st.Sessions != 2 {
		t.Fatalf("статистика: %+v", st)
	}
	if !strings.Contains(buf.String(), "каталог недоступен") {
		t.Errorf("нет Warn о каталоге: %q", buf.String())
	}
}

// TestRestore_FullChainNameMismatch — вставили кассету с чужим именем:
// ChainMismatchError сразу, вторая часть не читается.
func TestRestore_FullChainNameMismatch(t *testing.T) {
	h, _, _ := chainSetup(t, false)
	h.changer.Request = func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		return testutil.NewFakeTape(), domain.TapeLabel{Name: "T3"}, nil
	}

	_, err := h.uc.Full(context.Background())
	var mm *domain.ChainMismatchError
	if !errors.As(err, &mm) || mm.Expected != "T2" || mm.Got != "T3" {
		t.Fatalf("Full: %v; want ChainMismatchError{T2, T3}", err)
	}
	if st := h.dest; st == nil {
		t.Fatal("dest потерян")
	}
	if h.prog.fails != 0 {
		t.Errorf("fails = %d; want 0 (Fail публикует вызывающий)", h.prog.fails)
	}
}

// TestRestore_FullChainContinuesMismatch — имя совпало, но обратная
// ссылка индекса ведёт на другую кассету: ChainMismatchError.
func TestRestore_FullChainContinuesMismatch(t *testing.T) {
	h, _, _ := chainSetup(t, false)
	h.codec.Headers[0].Continues = "uuid-чужой"

	_, err := h.uc.Full(context.Background())
	var mm *domain.ChainMismatchError
	if !errors.As(err, &mm) || mm.Expected != "tape-uuid" || mm.Got != "uuid-чужой" {
		t.Fatalf("Full: %v; want ChainMismatchError{tape-uuid, uuid-чужой}", err)
	}
}

// TestRestore_FullChainNoHeader — на новой кассете нет сессии 1
// (пустой индекс): ошибка до восстановления данных.
func TestRestore_FullChainNoHeader(t *testing.T) {
	h, _, _ := chainSetup(t, false)
	h.codec.Headers = nil

	_, err := h.uc.Full(context.Background())
	if !errors.Is(err, &domain.EmptyIndexError{}) {
		t.Fatalf("Full: %v; want EmptyIndexError", err)
	}
}

// TestRestore_FullChainCancelWhileWaiting — контекст отменен во время
// ожидания оператора: восстановление завершается ошибкой отмены.
func TestRestore_FullChainCancelWhileWaiting(t *testing.T) {
	h, _, _ := chainSetup(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	h.changer.Request = func(ctx context.Context, _ port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		cancel()
		<-ctx.Done()
		return nil, domain.TapeLabel{}, ctx.Err()
	}

	_, err := h.uc.Full(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Full: %v; want context.Canceled", err)
	}
}

// TestRestore_FullChainSwapErrors — сбои закрытия кассеты и запроса
// следующей завершают восстановление ошибкой.
func TestRestore_FullChainSwapErrors(t *testing.T) {
	boom := errors.New("changer boom")

	h, _, _ := chainSetup(t, false)
	h.changer.Close = func(context.Context, port.Tape) error { return boom }
	if _, err := h.uc.Full(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("сбой CloseTape: %v; want boom", err)
	}

	h2, _, _ := chainSetup(t, false)
	h2.changer.Request = func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		return nil, domain.TapeLabel{}, boom
	}
	if _, err := h2.uc.Full(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("сбой RequestNext: %v; want boom", err)
	}
}

// failFromTape — лента, у которой Rewind/ForwardFilemarks падают
// начиная с N-го вызова (1-based; 0 — никогда).
type failFromTape struct {
	*testutil.FakeTape
	rewindFrom int
	fsfFrom    int
	rewinds    int
	fsfs       int
	err        error
}

func (tp *failFromTape) Rewind(ctx context.Context) error {
	tp.rewinds++
	if tp.rewindFrom > 0 && tp.rewinds >= tp.rewindFrom {
		return tp.err
	}
	return tp.FakeTape.Rewind(ctx)
}

func (tp *failFromTape) ForwardFilemarks(ctx context.Context, n int) error {
	tp.fsfs++
	if tp.fsfFrom > 0 && tp.fsfs >= tp.fsfFrom {
		return tp.err
	}
	return tp.FakeTape.ForwardFilemarks(ctx, n)
}

// TestRestore_FullChainPositioningFails — сбои позиционирования новой
// кассеты (первая и вторая перемотка/пропуск ярлыка) — ошибки.
func TestRestore_FullChainPositioningFails(t *testing.T) {
	boom := errors.New("mtio boom")
	cases := []struct {
		name        string
		rewindFrom  int
		fsfFrom     int
		wantInError string
	}{
		{"первая перемотка", 1, 0, "перемотка"},
		{"первый пропуск ярлыка", 0, 1, "пропуск ярлыка"},
		{"вторая перемотка", 2, 0, "перемотка"},
		{"второй пропуск ярлыка", 0, 2, "пропуск ярлыка"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := chainSetup(t, false)
			flaky := &failFromTape{
				FakeTape:   labeledChainTape(t, h.codec, "T2", "tape-uuid-2"),
				rewindFrom: tc.rewindFrom, fsfFrom: tc.fsfFrom, err: boom,
			}
			h.changer.Request = func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
				return flaky, domain.TapeLabel{Name: "T2", UUID: "tape-uuid-2"}, nil
			}
			_, err := h.uc.Full(context.Background())
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), tc.wantInError) {
				t.Fatalf("Full: %v; want boom при %s", err, tc.wantInError)
			}
		})
	}
}

// TestChainFollower_NoCatalogNoProgress — напрямую: каталог и
// прогресс не обязательны (DR-семантика, readtest без прогресса).
func TestChainFollower_NoCatalogNoProgress(t *testing.T) {
	h, tape2, _ := chainSetup(t, false)
	ch := &testutil.FuncChanger{
		Request: func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			return tape2, domain.TapeLabel{Name: "T2", UUID: "tape-uuid-2"}, nil
		},
	}
	f := &restore.ChainFollower{Changer: ch, Codec: h.codec, Log: testutil.NoopLogger()}
	next, err := f.Follow(context.Background(),
		restore.TapeState{Tape: h.tape, Label: domain.TapeLabel{Name: "T1", UUID: "tape-uuid"}},
		h.codec.Cont)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if next.Label.Name != "T2" || next.Tape != port.Tape(tape2) {
		t.Fatalf("новая кассета: %+v", next.Label)
	}
}

// TestRestore_SelectiveFromPart2 — файл части 2 восстанавливается
// selective'ом по sessionID части, если кассета части в приводе.
func TestRestore_SelectiveFromPart2(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id, err := h.cat.CreateSession(ctx, domain.Session{
		TapeUUID: "tape-uuid", Num: 1, Type: domain.SessionFull,
		Timestamp: 100, JobRunID: "run", Part: 2,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.cat.SaveFiles(ctx, id, []domain.FileMeta{meta("/part2/b")}); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	h.codec.Queue = append(h.codec.Queue, []domain.FileMeta{meta("/part2/b")})
	for i := 0; i < 2; i++ {
		if err := h.tape.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF: %v", err)
		}
	}

	st, err := h.uc.Selective(ctx, id, nil)
	if err != nil {
		t.Fatalf("Selective: %v", err)
	}
	if st.Files != 1 {
		t.Fatalf("Files = %d; want 1", st.Files)
	}
	// MTFSF(2*1-1) = 1 — сессия 1 кассеты части
	if len(h.tape.fsf) != 1 || h.tape.fsf[0] != 1 {
		t.Errorf("ForwardFilemarks args = %v; want [1]", h.tape.fsf)
	}
}

// TestRestore_SelectiveWrongTape — сессия части лежит на другой
// кассете: ошибка с именем кассеты, которую нужно вставить; сбой
// каталога при поиске имени — подсказка с UUID (best-effort).
func TestRestore_SelectiveWrongTape(t *testing.T) {
	ctx := context.Background()
	t.Run("кассета известна каталогу", func(t *testing.T) {
		h := newHarness(t)
		if err := h.cat.RegisterTape(ctx, "tape-uuid-2", "T2", 1); err != nil {
			t.Fatalf("RegisterTape: %v", err)
		}
		id, err := h.cat.CreateSession(ctx, domain.Session{
			TapeUUID: "tape-uuid-2", Num: 1, Type: domain.SessionFull,
			Timestamp: 100, JobRunID: "run", Part: 2,
		})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		_, err = h.uc.Selective(ctx, id, nil)
		if err == nil || !strings.Contains(err.Error(), "вставьте T2") {
			t.Fatalf("Selective: %v; want подсказка \"вставьте T2\"", err)
		}
		if !errors.Is(err, &domain.LabelMismatchError{}) {
			t.Fatalf("Selective: %v; want LabelMismatchError", err)
		}
		if len(h.tape.fsf) != 0 {
			t.Errorf("позиционирование не должно выполняться: %v", h.tape.fsf)
		}
	})
	t.Run("каталог не знает имени", func(t *testing.T) {
		h := newHarness(t)
		if err := h.cat.RegisterTape(ctx, "tape-uuid-2", "T2", 1); err != nil {
			t.Fatalf("RegisterTape: %v", err)
		}
		id, err := h.cat.CreateSession(ctx, domain.Session{
			TapeUUID: "tape-uuid-2", Num: 1, Type: domain.SessionFull,
			Timestamp: 100, JobRunID: "run", Part: 2,
		})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		fc := &failCat{MemCatalog: h.cat, getTape: errors.New("db down")}
		h.uc = restore.New(h.tape, h.codec, fc, h.dest, nil, testutil.NoopLogger(), nil)
		_, err = h.uc.Selective(ctx, id, nil)
		if err == nil || !strings.Contains(err.Error(), "вставьте tape-uuid-2") {
			t.Fatalf("Selective: %v; want подсказка с UUID", err)
		}
	})
}
