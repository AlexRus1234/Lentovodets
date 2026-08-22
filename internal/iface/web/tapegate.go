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

// Сериализация доступа к устройству ленты в демоне. Драйвер st
// допускает ровно один открытый дескриптор /dev/nst*: второй open
// получает EBUSY. До gate веб-слой открывал устройство из семи мест
// без координации (probe статуса каждые 15 с, tape info/eject/format,
// задачи) — параллельные запросы сталкивались на open.

package web

import "sync"

// tapeGate — единственный владелец очереди на устройство ленты.
// Два класса владельцев:
//   - короткие операции (probe статуса, tape info/format/eject):
//     выполняются по одной; при активной фоновой задаче откатываются
//     tapeBusyError (HTTP 409), а не ждут часами;
//   - фоновая задача (backup/restore): захватывает устройство до конца
//     (часы), перед захватом дождавшись текущей короткой операции.
type tapeGate struct {
	mu       sync.Mutex // охраняет taskHeld
	taskHeld bool       // устройство владеет фоновая задача

	shortMu sync.Mutex // одна короткая операция в момент времени
}

// tapeBusyError — устройство захвачено фоновой задачей; операция не
// выполнена (web: 409 task_running).
type tapeBusyError struct{}

// Error реализует интерфейс error.
func (tapeBusyError) Error() string {
	return "устройство занято фоновой задачей"
}

// withOp выполняет короткую операцию над лентой (открытие и закрытие —
// внутри fn). Отказывает tapeBusyError, если устройство у фоновой
// задачи; параллельные короткие операции выполняются строго по одной.
func (g *tapeGate) withOp(fn func() error) error {
	if g.heldByTask() {
		return tapeBusyError{}
	}
	g.shortMu.Lock()
	defer g.shortMu.Unlock()
	// Задача могла выставить taskHeld между первой проверкой и
	// захватом shortMu — перепроверяем под блокировкой.
	if g.heldByTask() {
		return tapeBusyError{}
	}
	return fn()
}

// probe проверяет доступность устройства (открытие/закрытие — внутри
// fn). Занятость задачей или короткой операцией считается
// доступностью: устройство открыто владельцем, ждать смысла нет.
func (g *tapeGate) probe(fn func() error) bool {
	if g.heldByTask() {
		return true
	}
	if !g.shortMu.TryLock() {
		return true // идёт короткая операция — устройство открыто
	}
	defer g.shortMu.Unlock()
	if g.heldByTask() {
		return true
	}
	return fn() == nil
}

// reserveTask резервирует устройство для фоновой задачи до её запуска:
// taskHeld выставляется немедленно (новые короткие операции получают
// 409), чтобы между регистрацией задачи в реестре и началом её
// горутины ни одна короткая операция не успела открыть устройство
// (гонка → EBUSY у задачи). false — устройством уже владеет задача.
func (g *tapeGate) reserveTask() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.taskHeld {
		return false
	}
	g.taskHeld = true
	return true
}

// unreserveTask снимает резерв, если задача так и не стартовала (сбой
// регистрации в реестре); shortMu в этот момент не взят.
func (g *tapeGate) unreserveTask() {
	g.mu.Lock()
	g.taskHeld = false
	g.mu.Unlock()
}

// waitShortOps дожидается текущей короткой операции (shortMu).
// Вызывается горутиной зарезервированной задачи до открытия ленты;
// задача обязана завершиться releaseTask.
func (g *tapeGate) waitShortOps() {
	g.shortMu.Lock()
}

// acquireTask захватывает устройство на время фоновой задачи:
// reserveTask + waitShortOps. Вызывается единственной задачей —
// вторую не пускают реестр задач (StartIfIdle) и reserveTask;
// shortMu не реентерабелен.
func (g *tapeGate) acquireTask() {
	g.reserveTask()
	g.waitShortOps()
}

// releaseTask освобождает устройство после фоновой задачи; обязателен
// после каждого acquireTask (defer).
func (g *tapeGate) releaseTask() {
	g.shortMu.Unlock()
	g.mu.Lock()
	g.taskHeld = false
	g.mu.Unlock()
}

// heldByTask сообщает, владеет ли устройством фоновая задача.
func (g *tapeGate) heldByTask() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.taskHeld
}
