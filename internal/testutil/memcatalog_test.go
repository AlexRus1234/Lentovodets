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

package testutil_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

func TestMemCatalog_TapeLifecycle(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()

	if _, err := cat.GetTapeByUUID(ctx, "u1"); !errors.Is(err, &domain.TapeNotFoundError{UUID: "u1"}) {
		t.Fatalf("GetTapeByUUID до регистрации: %v", err)
	}
	if err := cat.RegisterTape(ctx, "u1", "first", 100); err != nil {
		t.Fatalf("RegisterTape: %v", err)
	}
	if err := cat.RegisterTape(ctx, "u1", "renamed", 200); err != nil {
		t.Fatalf("RegisterTape (UPSERT): %v", err)
	}
	rec, err := cat.GetTapeByUUID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetTapeByUUID: %v", err)
	}
	if rec.Name != "renamed" || rec.FormattedAt != 200 {
		t.Errorf("после UPSERT: %+v; want renamed/200", rec)
	}
}

func TestMemCatalog_SessionFKAndLastNum(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()

	if _, err := cat.CreateSession(ctx, domain.Session{TapeUUID: "ghost"}); err == nil {
		t.Fatal("CreateSession с незарегистрированной кассетой: ожидалась ошибка FK")
	}
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)
	sess := domain.Session{TapeUUID: "u1", Num: 1, Type: domain.SessionFull, Timestamp: 10}
	id1, err := cat.CreateSession(ctx, sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id1 == 0 {
		t.Fatal("CreateSession вернул нулевой PK")
	}
	id2, err := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 7, Type: domain.SessionInc, Timestamp: 20})
	if err != nil {
		t.Fatalf("CreateSession(2): %v", err)
	}
	if id2 == id1 {
		t.Fatalf("PK не растёт: %d == %d", id1, id2)
	}
	num, err := cat.LastSessionNum(ctx, "u1")
	if err != nil || num != 7 {
		t.Fatalf("LastSessionNum = %d, %v; want 7", num, err)
	}
	num, err = cat.LastSessionNum(ctx, "unknown")
	if err != nil || num != 0 {
		t.Fatalf("LastSessionNum(unknown) = %d, %v; want 0", num, err)
	}
}

func TestMemCatalog_SaveGetFilesBySession(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)
	id, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1"})

	files := []domain.FileMeta{
		{Path: "/b", State: domain.StateAdded},
		{Path: "/a", State: domain.StateModified},
	}
	if err := cat.SaveFiles(ctx, id, files); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	if err := cat.SaveFiles(ctx, id, nil); err != nil {
		t.Fatalf("SaveFiles(nil): %v", err)
	}
	if err := cat.SaveFiles(ctx, 999, files); err == nil {
		t.Fatal("SaveFiles в несуществующую сессию: ожидалась ошибка")
	}

	got, err := cat.GetFilesBySession(ctx, id)
	if err != nil {
		t.Fatalf("GetFilesBySession: %v", err)
	}
	if len(got) != 2 || got[0].Path != "/a" || got[1].Path != "/b" {
		t.Fatalf("файлы не отсортированы по пути: %+v", got)
	}
	if _, err := cat.GetFilesBySession(ctx, 999); !errors.Is(err, &domain.SessionNotFoundError{SessionID: 999}) {
		t.Fatalf("GetFilesBySession(999): %v", err)
	}
}

func TestMemCatalog_GetLatestFileStates(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)

	// Сессия 1: старое состояние /a и /b; сессия 2 (новее): /a изменён.
	id1, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 1, Timestamp: 10})
	_ = cat.SaveFiles(ctx, id1, []domain.FileMeta{
		{Path: "/a", Hash: "old-a", State: domain.StateAdded},
		{Path: "/b", Hash: "b", State: domain.StateAdded},
	})
	id2, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 2, Timestamp: 20})
	_ = cat.SaveFiles(ctx, id2, []domain.FileMeta{
		{Path: "/a", Hash: "new-a", State: domain.StateModified},
	})

	states, err := cat.GetLatestFileStates(ctx, []string{"/a", "/b", "/missing"})
	if err != nil {
		t.Fatalf("GetLatestFileStates: %v", err)
	}
	if got := states["/a"].Hash; got != "new-a" {
		t.Errorf("/a hash = %q; want new-a", got)
	}
	if got := states["/b"].Hash; got != "b" {
		t.Errorf("/b hash = %q; want b", got)
	}
	if _, ok := states["/missing"]; ok {
		t.Error("/missing не должно быть в результате")
	}
}

func TestMemCatalog_TimestampTieBrokenByID(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)
	// Одинаковый timestamp: новее сессия с большим ID.
	id1, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 1, Timestamp: 10})
	id2, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 2, Timestamp: 10})
	_ = cat.SaveFiles(ctx, id1, []domain.FileMeta{{Path: "/x", Hash: "first"}})
	_ = cat.SaveFiles(ctx, id2, []domain.FileMeta{{Path: "/x", Hash: "second"}})

	states, err := cat.GetLatestFileStates(ctx, []string{"/x"})
	if err != nil {
		t.Fatalf("GetLatestFileStates: %v", err)
	}
	if got := states["/x"].Hash; got != "second" {
		t.Errorf("/x hash = %q; want second", got)
	}
}

func TestMemCatalog_ListTapesSortedByName(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u2", "zeta", 0)
	_ = cat.RegisterTape(ctx, "u1", "alpha", 0)

	tapes, err := cat.ListTapes(ctx)
	if err != nil {
		t.Fatalf("ListTapes: %v", err)
	}
	if len(tapes) != 2 || tapes[0].Name != "alpha" || tapes[1].Name != "zeta" {
		t.Fatalf("ListTapes не отсортирован по имени: %+v", tapes)
	}
}

func TestMemCatalog_ListSessions(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)
	_ = cat.RegisterTape(ctx, "u2", "t2", 0)
	// Вставляем в обратном порядке: сортировка обязана исправить.
	id3, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u2", Num: 2, Timestamp: 30})
	id2, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u2", Num: 1, Timestamp: 20})
	id1, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 5, Timestamp: 10})

	all, err := cat.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("ListSessions(all): %v", err)
	}
	want := []int64{id1, id2, id3}
	if len(all) != len(want) {
		t.Fatalf("ListSessions(all): %d записей; want %d", len(all), len(want))
	}
	for i, id := range want {
		if all[i].ID != id {
			t.Errorf("all[%d].ID = %d; want %d (сортировка tape, num)", i, all[i].ID, id)
		}
	}

	one, err := cat.ListSessions(ctx, "u2")
	if err != nil {
		t.Fatalf("ListSessions(u2): %v", err)
	}
	if len(one) != 2 || one[0].Num != 1 || one[1].Num != 2 {
		t.Fatalf("ListSessions(u2): %+v", one)
	}
}

func TestMemCatalog_FileCopies(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)
	id1, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 1, Timestamp: 10})
	_ = cat.SaveFiles(ctx, id1, []domain.FileMeta{
		{Path: "/etc/hosts", Hash: "h1", State: domain.StateAdded},
		{Path: "/var/log/x", Hash: "l1", State: domain.StateAdded},
	})
	id2, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 2, Timestamp: 20})
	_ = cat.SaveFiles(ctx, id2, []domain.FileMeta{{Path: "/etc/hosts", Hash: "h2", State: domain.StateModified}})

	copies, err := cat.GetAllFileCopies(ctx, "/etc/hosts")
	if err != nil {
		t.Fatalf("GetAllFileCopies: %v", err)
	}
	if len(copies) != 2 || copies[0].Meta.Hash != "h2" || copies[1].Meta.Hash != "h1" {
		t.Fatalf("копии не отсортированы по убыванию времени: %+v", copies)
	}
	if copies[0].SessionNum != 2 || copies[0].TapeUUID != "u1" || copies[0].Timestamp != 20 {
		t.Errorf("поля копии: %+v", copies[0])
	}

	found, err := cat.SearchFiles(ctx, "hosts")
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("SearchFiles(hosts): %d записей; want 2", len(found))
	}
	found, err = cat.SearchFiles(ctx, "nothing")
	if err != nil || len(found) != 0 {
		t.Fatalf("SearchFiles(nothing): %d, %v; want 0", len(found), err)
	}
}

func TestMemCatalog_DeleteAndPrune(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog()
	_ = cat.RegisterTape(ctx, "u1", "t1", 0)
	id1, _ := cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 1, Timestamp: 10})
	_, _ = cat.CreateSession(ctx, domain.Session{TapeUUID: "u1", Num: 2, Timestamp: 100})
	_ = cat.SaveFiles(ctx, id1, []domain.FileMeta{{Path: "/old"}})

	if err := cat.DeleteSession(ctx, 42); !errors.Is(err, &domain.SessionNotFoundError{SessionID: 42}) {
		t.Fatalf("DeleteSession(42): %v", err)
	}
	if err := cat.DeleteSession(ctx, id1); err != nil {
		t.Fatalf("DeleteSession(id1): %v", err)
	}
	if _, err := cat.GetFilesBySession(ctx, id1); !errors.Is(err, &domain.SessionNotFoundError{SessionID: id1}) {
		t.Fatalf("файлы удалённой сессии: %v", err)
	}

	n, err := cat.PruneSessions(ctx, 50)
	if err != nil || n != 0 {
		t.Fatalf("PruneSessions(50) = %d, %v; want 0 (id2 новее)", n, err)
	}
	n, err = cat.PruneSessions(ctx, 200)
	if err != nil || n != 1 {
		t.Fatalf("PruneSessions(200) = %d, %v; want 1", n, err)
	}
	sessions, _ := cat.ListSessions(ctx, "")
	if len(sessions) != 0 {
		t.Fatalf("после prune осталось %d сессий", len(sessions))
	}
}

func TestMemCatalog_WithSnapshotSeedsState(t *testing.T) {
	ctx := context.Background()
	cat := testutil.NewMemCatalog().WithSnapshot(map[string]domain.FileMeta{
		"/etc/hosts": {Path: "/etc/hosts", Size: 1, Hash: "old", State: domain.StateAdded},
	})
	states, err := cat.GetLatestFileStates(ctx, []string{"/etc/hosts"})
	if err != nil {
		t.Fatalf("GetLatestFileStates: %v", err)
	}
	if got := states["/etc/hosts"].Hash; got != "old" {
		t.Fatalf("снимок не засеян: hash = %q", got)
	}
}

func TestMemCatalog_CanceledContext(t *testing.T) {
	cat := testutil.NewMemCatalog()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var catPort port.Catalog = cat
	_, err := catPort.GetTapeByUUID(ctx, "u")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("GetTapeByUUID: %v; want context.Canceled", err)
	}
}

func TestFixedClock(t *testing.T) {
	want := time.Unix(1700000000, 42)
	clock := testutil.FixedClock(want)
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("Now() = %v; want %v", got, want)
	}
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("Now() второй раз = %v; want %v (время не меняется)", got, want)
	}
}

func TestFixedRand(t *testing.T) {
	r := testutil.FixedRand("550e8400-e29b-41d4-a716-446655440000")
	u1, err := r.UUID4()
	if err != nil {
		t.Fatalf("UUID4: %v", err)
	}
	if u1 != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("UUID4 = %q", u1)
	}
	u2, _ := r.UUID4()
	if u2 != u1 {
		t.Fatalf("UUID4 при одном элементе должен повторяться: %q != %q", u2, u1)
	}

	cycled := testutil.FixedRand("a", "b")
	first, _ := cycled.UUID4()
	second, _ := cycled.UUID4()
	third, _ := cycled.UUID4()
	if first != "a" || second != "b" || third != "a" {
		t.Fatalf("последовательность a,b,a; got %s,%s,%s", first, second, third)
	}

	empty := testutil.FixedRand()
	u, err := empty.UUID4()
	if err != nil || u == "" {
		t.Fatalf("FixedRand() без аргументов: %q, %v", u, err)
	}
}

func TestFailingRand(t *testing.T) {
	boom := errors.New("boom")
	r := testutil.FailingRand(boom)
	if u, err := r.UUID4(); !errors.Is(err, boom) || u != "" {
		t.Fatalf("UUID4 = %q, %v; want \"\", boom", u, err)
	}
}
