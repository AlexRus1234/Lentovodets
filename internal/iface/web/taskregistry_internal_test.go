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

// Внутренние тесты пересчёта скорости задачи: среднее по фазе вместо
// EWMA по мгновенным сэмплам (сессия 19: 371 MiB/s на LTO-4).

package web

import (
	"math"
	"testing"
	"time"

	"lentovodec/internal/port"
)

// newSpeedTask — бегущая задача для прямой подачи update'ов.
func newSpeedTask() *Task {
	return &Task{state: taskRunning}
}

// TestTaskSpeed_BurstNotOverestimated — имитация пилы copyFromReader:
// «большая дельта байт за крошечное dt» (чанк 4 MiB из буфера
// копируется с память-скоростью) + «нулевая дельта за реальное dt»
// (блочный I/O ленты). Среднее по фазе не должно превышать физическую
// среднюю потока: 4 MiB / 34 мс ≈ 117.7 MiB/s (LTO-4 в стриминге);
// EWMA на этой пиле сходился к ~350+.
func TestTaskSpeed_BurstNotOverestimated(t *testing.T) {
	const (
		chunk   = 4 << 20               // domain.CopyBuffer
		chunkDT = time.Millisecond      // «память-скорость»
		ioDT    = 33 * time.Millisecond // ленточный I/O между чанками
		cycles  = 64
	)
	task := newSpeedTask()
	now := time.Unix(1700000000, 0)
	var processed int64
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: processed}, now)
	for i := 0; i < cycles; i++ {
		now = now.Add(chunkDT)
		processed += chunk
		task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: processed}, now)
		now = now.Add(ioDT)
		task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: processed}, now)
	}
	physAvg := float64(processed) / (cycles * (chunkDT + ioDT)).Seconds() / (1024 * 1024)
	got := task.Snapshot().SpeedMbps
	if got <= 0 || got > physAvg*(1+1e-9) {
		t.Errorf("speed=%v MiB/s, want (0, %.1f] — физическая средняя потока", got, physAvg)
	}
}

// TestTaskSpeed_PhaseChangeResetsBase — при смене фазы (scan → write)
// база сбрасывается: скорость отражает только write-этап, а не смесь
// быстрого скана и медленной записи.
func TestTaskSpeed_PhaseChangeResetsBase(t *testing.T) {
	task := newSpeedTask()
	now := time.Unix(1700000000, 0)
	task.update(port.ProgressUpdate{Phase: port.PhaseScan, ProcessedBytes: 0}, now)
	now = now.Add(time.Second)
	task.update(port.ProgressUpdate{Phase: port.PhaseScan, ProcessedBytes: 100 << 20}, now)
	// write считает байты с нуля (как реальный бекап: скан насчитал,
	// запись пошла заново).
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: 0}, now)
	now = now.Add(2 * time.Second)
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: 10 << 20}, now)

	got := task.Snapshot().SpeedMbps
	if got <= 0 || math.Abs(got-5) > 1e-9 { // 10 MiB за 2с, не 110 MiB «наследием» скана
		t.Errorf("speed=%v MiB/s, want 5 (только write-этап)", got)
	}
}

// TestTaskSpeed_ProcessedDecreaseResetsBase — processed в декодере
// локален для каждой сессии (Full-restore читает сессии подряд):
// при уменьшении счётчика база сбрасывается, скорость не уходит
// в минус/NaN, а пересчитывается от новой базы.
func TestTaskSpeed_ProcessedDecreaseResetsBase(t *testing.T) {
	task := newSpeedTask()
	now := time.Unix(1700000000, 0)
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: 0}, now)
	now = now.Add(time.Second)
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: 50 << 20}, now)
	// Новая сессия restore: счётчик меньше прошлого сэмпла.
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: 5 << 20}, now)
	now = now.Add(time.Second)
	task.update(port.ProgressUpdate{Phase: port.PhaseWrite, ProcessedBytes: 15 << 20}, now)

	got := task.Snapshot().SpeedMbps
	if math.IsNaN(got) || math.IsInf(got, 0) || got < 0 {
		t.Fatalf("speed=%v, want конечное неотрицательное", got)
	}
	if math.Abs(got-10) > 1e-9 { // 10 MiB за 1с от новой базы, не (15-50) MiB/2с < 0
		t.Errorf("speed=%v MiB/s, want 10 (от базы новой сессии)", got)
	}
}

// TestTaskSpeed_SingleUpdateZero — единственный update не даёт dt:
// скорость 0, без деления на ноль.
func TestTaskSpeed_SingleUpdateZero(t *testing.T) {
	task := newSpeedTask()
	task.update(port.ProgressUpdate{
		Phase:          port.PhaseWrite,
		ProcessedBytes: 42 << 20,
		TotalBytes:     42 << 20,
	}, time.Unix(1700000000, 0))
	if got := task.Snapshot().SpeedMbps; got != 0 || math.IsNaN(got) {
		t.Errorf("speed=%v, want 0 после единственного update", got)
	}
}
