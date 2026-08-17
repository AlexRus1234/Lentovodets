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

// Тесты readtest по цепочке кассет (сессия 6 плана spanning):
// отчёт по каждой кассете, сверка при смене, сбои смены.

package catalog_test

import (
	"context"
	"errors"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// chainTape — свежая лента с ярлыком name/uuid и filemark'ами одной
// сессии (модель кассеты цепочки).
func chainTape(t *testing.T, codec *testutil.FakeCodec, name, uuid string) *testutil.FakeTape {
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
	for i := 0; i < 3; i++ {
		if err := tp.WriteEOF(ctx); err != nil {
			t.Fatalf("WriteEOF: %v", err)
		}
	}
	return tp
}

// readTestChainSetup — кассета u1 с одной сессией и указателем
// продолжения на u2; changer выдаёт кассету u2.
func readTestChainSetup(t *testing.T) (*testutil.FakeCodec, *testutil.FakeTape, *testutil.FuncChanger, port.Catalog) {
	t.Helper()
	cat, _, _ := seedCatalog(t)
	codec := &testutil.FakeCodec{}
	tape := labeledTape(t, codec)
	codec.Queue = [][]domain.FileMeta{
		{{Path: "/a", State: domain.StateAdded, Size: 10}},
		{{Path: "/b", State: domain.StateAdded, Size: 20}},
	}
	codec.ContOnCall = 2
	codec.Cont = &domain.ContinuationError{
		JobRunID: "run", SessionNum: 2, Part: 2, NextTapeName: "u2",
	}
	codec.Headers = []port.SessionHeader{{
		SessionNum: 1, Type: domain.SessionFull, JobRunID: "run",
		Part: 2, Continues: "u1",
	}}
	tape2 := chainTape(t, codec, "u2", "uuid-2")
	ch := &testutil.FuncChanger{
		Request: func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			return tape2, domain.TapeLabel{
				Magic: domain.Magic, FormatVersion: domain.FormatVersion,
				Name: "u2", UUID: "uuid-2",
			}, nil
		},
	}
	return codec, tape, ch, cat
}

// TestCatalog_ReadTestFollowsChain — отчёт по обеим кассетам цепочки.
func TestCatalog_ReadTestFollowsChain(t *testing.T) {
	codec, tape, ch, cat := readTestChainSetup(t)
	uc := newChainCatalogUC(cat, tape, codec, ch)

	reports, err := uc.ReadTest(context.Background())
	if err != nil {
		t.Fatalf("ReadTest: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("отчётов %d; want 2: %+v", len(reports), reports)
	}
	if reports[0].Name != "t-1" || reports[0].Sessions != 1 || reports[0].Files != 1 {
		t.Errorf("отчёт кассеты 1: %+v", reports[0])
	}
	if reports[1].Name != "u2" || reports[1].Sessions != 1 || reports[1].Files != 1 || reports[1].Bytes != 20 {
		t.Errorf("отчёт кассеты 2: %+v", reports[1])
	}
	if len(ch.Requests) != 1 || ch.Requests[0].Reason != port.ReasonRestore {
		t.Errorf("запросы смены: %+v", ch.Requests)
	}
}

// TestCatalog_ReadTestChainMismatch — вставлена не та кассета:
// ChainMismatchError, отчёт содержит только первую кассету.
func TestCatalog_ReadTestChainMismatch(t *testing.T) {
	codec, tape, ch, cat := readTestChainSetup(t)
	ch.Request = func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		return testutil.NewFakeTape(), domain.TapeLabel{Name: "другая"}, nil
	}
	uc := newChainCatalogUC(cat, tape, codec, ch)

	reports, err := uc.ReadTest(context.Background())
	var mm *domain.ChainMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("ReadTest: %v; want ChainMismatchError", err)
	}
	if len(reports) != 1 || reports[0].Name != "t-1" {
		t.Fatalf("отчёты: %+v; want только первая кассета", reports)
	}
}

// TestCatalog_ReadTestChainSwapFails — сбои закрытия/запроса кассеты
// завершают readtest ошибкой.
func TestCatalog_ReadTestChainSwapFails(t *testing.T) {
	boom := errors.New("changer boom")

	codec, tape, ch, cat := readTestChainSetup(t)
	ch.Close = func(context.Context, port.Tape) error { return boom }
	uc := newChainCatalogUC(cat, tape, codec, ch)
	if _, err := uc.ReadTest(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("сбой CloseTape: %v; want boom", err)
	}

	codec2, tape2, ch2, cat2 := readTestChainSetup(t)
	ch2.Request = func(context.Context, port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		return nil, domain.TapeLabel{}, boom
	}
	uc2 := newChainCatalogUC(cat2, tape2, codec2, ch2)
	if _, err := uc2.ReadTest(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("сбой RequestNext: %v; want boom", err)
	}
}

// TestCatalog_ReadTestChainCancelWhileWaiting — отмена во время
// ожидания кассеты.
func TestCatalog_ReadTestChainCancelWhileWaiting(t *testing.T) {
	codec, tape, ch, cat := readTestChainSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch.Request = func(ctx context.Context, _ port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
		cancel()
		<-ctx.Done()
		return nil, domain.TapeLabel{}, ctx.Err()
	}
	uc := newChainCatalogUC(cat, tape, codec, ch)

	if _, err := uc.ReadTest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadTest: %v; want context.Canceled", err)
	}
}
