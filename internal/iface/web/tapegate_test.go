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

// Внутренние тесты tapeGate: сериализация коротких операций, исключение
// фоновой задачей, ожидание in-flight операции задачей, probe.

package web

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTapeGate_ShortOpsSerialized — параллельные withOp выполняются
// строго по одной (st-драйвер: один открытый дескриптор).
func TestTapeGate_ShortOpsSerialized(t *testing.T) {
	var g tapeGate
	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.withOp(func() error {
				cur := inside.Add(1)
				for {
					m := maxInside.Load()
					if cur <= m || maxInside.CompareAndSwap(m, cur) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				inside.Add(-1)
				return nil
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("withOp[%d]: %v", i, err)
		}
	}
	if got := maxInside.Load(); got != 1 {
		t.Fatalf("максимум одновременных операций = %d, want 1", got)
	}
}

// TestTapeGate_TaskBlocksShortOps — активная задача откатывает withOp
// понятной tapeBusyError, probe считает устройство доступным.
func TestTapeGate_TaskBlocksShortOps(t *testing.T) {
	var g tapeGate
	g.acquireTask()
	defer g.releaseTask()

	err := g.withOp(func() error { return nil })
	var busy tapeBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("withOp при задаче: %v; want tapeBusyError", err)
	}
	if !g.probe(func() error { return nil }) {
		t.Fatal("probe при задаче: false, want true (устройство открыто задачей)")
	}
}

// TestTapeGate_ProbeOpenFails — probe без задачи возвращает false при
// ошибке открытия (нет носителя, прав).
func TestTapeGate_ProbeOpenFails(t *testing.T) {
	var g tapeGate
	if g.probe(func() error { return errors.New("open /dev/nst0: no medium found") }) {
		t.Fatal("probe при ошибке открытия: true, want false")
	}
}

// TestTapeGate_TaskWaitsInflightShort — задача выставляет taskHeld до
// ожидания: новые короткие операции откатываются сразу, а сама ждёт
// завершения текущей; после releaseTask операции снова работают.
func TestTapeGate_TaskWaitsInflightShort(t *testing.T) {
	var g tapeGate
	releaseShort := make(chan struct{})
	shortEntered := make(chan struct{})
	shortDone := make(chan struct{})
	go func() {
		err := g.withOp(func() error {
			close(shortEntered)
			<-releaseShort
			return nil
		})
		if err != nil {
			t.Errorf("withOp: %v", err)
		}
		close(shortDone)
	}()
	<-shortEntered // короткая операция держит устройство

	// Короткая операция ещё держит устройство. Задача стартует
	// параллельно и выставляет taskHeld ДО ожидания shortMu — ждём
	// этого момента явно, иначе первый withOp встанет в очередь за
	// короткой операцией вместо немедленного busy.
	taskAcquired := make(chan struct{})
	mayRelease := make(chan struct{})
	taskDone := make(chan struct{})
	go func() {
		g.acquireTask()
		close(taskAcquired)
		<-mayRelease
		g.releaseTask()
		close(taskDone)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !g.heldByTask() {
		if time.Now().After(deadline) {
			t.Fatal("задача не выставила taskHeld за 2с")
		}
		time.Sleep(time.Millisecond)
	}

	// Пока задача не отпустила устройство (а она ждёт short),
	// новый withOp откатывается, probe проходит без ожидания.
	if !errors.As(g.withOp(func() error { return nil }), new(tapeBusyError)) {
		t.Fatal("withOp при ожидающей задаче: нет tapeBusyError")
	}
	if !g.probe(func() error { return nil }) {
		t.Fatal("probe во время ожидания задачи: false, want true")
	}

	close(releaseShort)
	<-shortDone
	select {
	case <-taskAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("задача не дождалась короткой операции за 2с")
	}

	// Задача владеет устройством — операции всё ещё отклоняются.
	if !errors.As(g.withOp(func() error { return nil }), new(tapeBusyError)) {
		t.Fatal("withOp при владеющей задаче: нет tapeBusyError")
	}
	close(mayRelease)
	<-taskDone

	// Устройство освобождено — операции работают.
	if err := g.withOp(func() error { return nil }); err != nil {
		t.Fatalf("withOp после releaseTask: %v", err)
	}
}

// TestTapeGate_BusyErrorText — текст ошибки понятен оператору.
func TestTapeGate_BusyErrorText(t *testing.T) {
	if got := (tapeBusyError{}).Error(); got != "устройство занято фоновой задачей" {
		t.Fatalf("текст tapeBusyError: %q", got)
	}
}

// TestTapeGate_ReserveBeforeLaunch — резерв задачи (startTapeTask)
// отклоняет короткие операции ещё до запуска горутины задачи: гонка
// «задача зарегистрирована, гейт не взят» больше не даёт короткой
// операции открыть устройство. Второй резерв невозможен; unreserve
// возвращает всё как было.
func TestTapeGate_ReserveBeforeLaunch(t *testing.T) {
	var g tapeGate
	if !g.reserveTask() {
		t.Fatal("первый reserveTask: false, want true")
	}
	if g.reserveTask() {
		t.Fatal("второй reserveTask: true, want false (устройство зарезервировано)")
	}
	// зарезервировано — короткие операции отклоняются, probe «занято»
	if !errors.As(g.withOp(func() error { return nil }), new(tapeBusyError)) {
		t.Fatal("withOp после reserve: нет tapeBusyError")
	}
	if !g.probe(func() error { return nil }) {
		t.Fatal("probe после reserve: false, want true")
	}

	// отмена старта: unreserve возвращает устройство коротким операциям
	g.unreserveTask()
	if err := g.withOp(func() error { return nil }); err != nil {
		t.Fatalf("withOp после unreserve: %v", err)
	}
	if !g.reserveTask() {
		t.Fatal("reserveTask после unreserve: false, want true")
	}
	g.unreserveTask()
}
