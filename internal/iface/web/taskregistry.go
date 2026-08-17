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

// Реестр фоновых задач демона: in-memory map + горутины backup/restore.
// Не персистентен (docs/ARCHITECTURE.md §5): перезапуск демона теряет
// активные задачи, но данные на ленте уже записаны или нет — по фазе.

package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"lentovodec/internal/port"
)

// Состояния задачи (SPEC §6.4).
const (
	taskRunning = "running"
	taskSuccess = "success"
	taskError   = "error"
)

// maxTaskLogs — размер кольцевого буфера лога задачи (SPEC §6.4:
// «последние 50 строк»).
const maxTaskLogs = 50

// minLogInterval — минимальный интервал между строками лога задачи,
// чтобы Update-поток не заливал буфер (2 Гц, как у прогресса).
const minLogInterval = 500 * time.Millisecond

// Task — фоновая задача демона (backup или restore).
type Task struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // backup | restore

	mu          sync.Mutex
	state       string
	phase       string
	currentFile string
	processed   int64
	total       int64
	errText     string
	message     string // текст оператору (смена кассеты spanning)
	startedAt   time.Time
	finishedAt  time.Time
	logs        []string
	lastLogAt   time.Time
	lastSample  time.Time
	lastBytes   int64
	speedBps    float64
}

// taskProgressJSON — прогресс задачи по SPEC §6.4.
type taskProgressJSON struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	State          string   `json:"state"`
	Phase          string   `json:"phase"`
	CurrentFile    string   `json:"current_file"`
	ProcessedBytes int64    `json:"processed_bytes"`
	TotalBytes     int64    `json:"total_bytes"`
	Percent        float64  `json:"percent"`
	SpeedMbps      float64  `json:"speed_mbps"`
	Logs           []string `json:"logs"`
	Error          string   `json:"error"`
	Message        string   `json:"message"`
}

// Snapshot возвращает неизменяемый снимок прогресса задачи.
func (t *Task) Snapshot() taskProgressJSON {
	t.mu.Lock()
	defer t.mu.Unlock()
	var percent float64
	if t.total > 0 {
		percent = float64(t.processed) / float64(t.total) * 100
		if percent > 100 {
			percent = 100
		}
	}
	logs := make([]string, len(t.logs))
	copy(logs, t.logs)
	return taskProgressJSON{
		ID:             t.ID,
		Kind:           t.Kind,
		State:          t.state,
		Phase:          t.phase,
		CurrentFile:    t.currentFile,
		ProcessedBytes: t.processed,
		TotalBytes:     t.total,
		Percent:        percent,
		SpeedMbps:      t.speedBps / (1024 * 1024),
		Logs:           logs,
		Error:          t.errText,
		Message:        t.message,
	}
}

// update применяет очередной ProgressUpdate; строки лога пишутся не
// чаще двух в секунду.
func (t *Task) update(u port.ProgressUpdate, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != taskRunning {
		return
	}
	dt := now.Sub(t.lastSample)
	if dt > 0 && u.ProcessedBytes >= t.lastBytes {
		inst := float64(u.ProcessedBytes-t.lastBytes) / dt.Seconds()
		if t.speedBps == 0 {
			t.speedBps = inst
		} else {
			t.speedBps = 0.5*t.speedBps + 0.5*inst // сглаживание
		}
	}
	t.lastSample, t.lastBytes = now, u.ProcessedBytes
	t.phase, t.currentFile = u.Phase, u.CurrentFile
	t.processed, t.total = u.ProcessedBytes, u.TotalBytes
	if u.Message != "" {
		t.message = u.Message
		t.appendLogLocked(u.Message) // события оператора — без троттлинга
		return
	}
	if now.Sub(t.lastLogAt) >= minLogInterval {
		t.appendLogLocked(fmt.Sprintf("[%s] %s (%s / %s)",
			u.Phase, u.CurrentFile, humanBytes(u.ProcessedBytes), humanBytes(u.TotalBytes)))
		t.lastLogAt = now
	}
}

// finishSuccess фиксирует успешное завершение.
func (t *Task) finishSuccess(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != taskRunning {
		return
	}
	t.state = taskSuccess
	t.finishedAt = now
	t.appendLogLocked("задача завершена успешно")
}

// finishError фиксирует завершение с ошибкой.
func (t *Task) finishError(err error, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != taskRunning {
		return
	}
	t.state = taskError
	t.finishedAt = now
	t.errText = err.Error()
	t.appendLogLocked("задача завершена ошибкой: " + t.errText)
}

// appendLogLocked добавляет строку в буфер. Вызывать под мьютексом.
func (t *Task) appendLogLocked(line string) {
	t.logs = append(t.logs, line)
	if len(t.logs) > maxTaskLogs {
		t.logs = t.logs[len(t.logs)-maxTaskLogs:]
	}
}

// taskProgress — port.ProgressReporter, пишущий в задачу.
type taskProgress struct {
	task  *Task
	clock port.Clock
}

// NewTaskProgress создаёт репортёр прогресса для задачи.
func NewTaskProgress(task *Task, clock port.Clock) port.ProgressReporter {
	return &taskProgress{task: task, clock: clock}
}

// Update публикует снимок прогресса.
func (p *taskProgress) Update(u port.ProgressUpdate) {
	p.task.update(u, p.clock.Now())
}

// Done фиксирует успех.
func (p *taskProgress) Done() {
	p.task.finishSuccess(p.clock.Now())
}

// Fail фиксирует ошибку.
func (p *taskProgress) Fail(err error) {
	p.task.finishError(err, p.clock.Now())
}

// TaskRegistry — общий реестр задач демона.
type TaskRegistry struct {
	mu    sync.Mutex
	tasks map[string]*Task
	wg    sync.WaitGroup
}

// NewTaskRegistry создаёт пустой реестр.
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{tasks: make(map[string]*Task)}
}

// StartIfIdle регистрирует новую задачу и запускает fn в фоновой
// горутине. Отказ, если уже есть бегущая задача: лента одна, параллельные
// ленточные операции бессмысленны. Проверка и регистрация атомарны.
func (r *TaskRegistry) StartIfIdle(id, kind string, fn func(*Task)) error {
	r.mu.Lock()
	if r.hasActiveLocked() {
		r.mu.Unlock()
		return errors.New("уже есть активная задача; дождитесь завершения")
	}
	task := r.startLocked(id, kind)
	r.mu.Unlock()

	r.launch(task, fn)
	return nil
}

// Start регистрирует задачу без проверки занятости (для сценариев,
// где несколько задач допустимы — тесты реестра).
func (r *TaskRegistry) Start(id, kind string, fn func(*Task)) *Task {
	r.mu.Lock()
	task := r.startLocked(id, kind)
	r.mu.Unlock()

	r.launch(task, fn)
	return task
}

// startLocked создаёт и запоминает задачу. Вызывать под мьютексом.
func (r *TaskRegistry) startLocked(id, kind string) *Task {
	task := &Task{
		ID:        id,
		Kind:      kind,
		state:     taskRunning,
		phase:     port.PhaseScan,
		startedAt: time.Now(),
	}
	r.tasks[id] = task
	return task
}

// launch запускает fn в фоновой горутине с учётом WaitAll.
func (r *TaskRegistry) launch(task *Task, fn func(*Task)) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		fn(task)
	}()
}

// hasActiveLocked — есть ли бегущая задача. Вызывать под мьютексом.
func (r *TaskRegistry) hasActiveLocked() bool {
	for _, t := range r.tasks {
		if t.stateLocked() == taskRunning {
			return true
		}
	}
	return false
}

// stateLocked — состояние задачи без полной копии (для фильтров).
func (t *Task) stateLocked() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Get возвращает задачу по идентификатору.
func (r *TaskRegistry) Get(id string) (*Task, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[id]
	return t, ok
}

// HasActive сообщает, есть ли хоть одна бегущая задача.
func (r *TaskRegistry) HasActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hasActiveLocked()
}

// Active возвращает бегущие задачи, старые первыми.
func (r *TaskRegistry) Active() []*Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var active []*Task
	for _, t := range r.tasks {
		if t.stateLocked() == taskRunning {
			active = append(active, t)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		si, sj := active[i].startedAtTS(), active[j].startedAtTS()
		if si != sj {
			return si < sj
		}
		return active[i].ID < active[j].ID
	})
	return active
}

// WaitAll ждёт завершения всех фоновых задач или отмены ctx.
func (r *TaskRegistry) WaitAll(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startedAtTS — Unix-время старта (для сортировки).
func (t *Task) startedAtTS() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() {
		return 0
	}
	return t.startedAt.UnixNano()
}

// humanBytes — компактное человекочитаемое число байт для лога.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// defaultTaskID — идентификатор задачи вида "task-XXXXXXXX" из
// crypto/rand; при отказе источника — по таймеру (уникальность важнее
// энтропии).
func defaultTaskID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("task-%08d", time.Now().UnixNano()%1e8)
	}
	return "task-" + hex.EncodeToString(buf)
}
