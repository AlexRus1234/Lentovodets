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

// Состояния задачи (SPEC §6.4). awaiting_tape — расширение spanning:
// задача приостановлена, оператору нужна следующая кассета цепочки;
// продолжение — POST /api/tasks/{id}/continue (сессия 7 плана).
const (
	taskRunning      = "running"
	taskAwaitingTape = "awaiting_tape"
	taskSuccess      = "success"
	taskError        = "error"
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
	message     string      // текст оператору (смена кассеты spanning)
	continueCh  chan string // ответ оператора в awaiting_tape (буфер 1)
	suggested   string      // предложенное имя кассеты для продолжения
	startedAt   time.Time
	finishedAt  time.Time
	logs        []string
	lastLogAt   time.Time
	baseSample  time.Time
	baseBytes   int64
	lastBytes   int64
	speedBps    float64
	job         string
	bytes       int64
	files       int
	tapes       []string
	onFinish    func(TaskFinishSnapshot)
	version     string
}

// TaskFinishSnapshot — payload webhook о завершении фоновой задачи.
type TaskFinishSnapshot struct {
	Event      string    `json:"event"`
	TaskID     string    `json:"task_id"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	Error      string    `json:"error"`
	Job        string    `json:"job"`
	Bytes      int64     `json:"bytes"`
	Files      int       `json:"files"`
	Tapes      []string  `json:"tapes"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Version    string    `json:"version"`
}

// taskProgressJSON — прогресс задачи по SPEC §6.4.
type taskProgressJSON struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	State             string   `json:"state"`
	Phase             string   `json:"phase"`
	CurrentFile       string   `json:"current_file"`
	ProcessedBytes    int64    `json:"processed_bytes"`
	TotalBytes        int64    `json:"total_bytes"`
	Percent           float64  `json:"percent"`
	SpeedMbps         float64  `json:"speed_mbps"`
	Logs              []string `json:"logs"`
	Error             string   `json:"error"`
	Message           string   `json:"message"`
	SuggestedTapeName string   `json:"suggested_tape_name,omitempty"`
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
		ID:                t.ID,
		Kind:              t.Kind,
		State:             t.state,
		Phase:             t.phase,
		CurrentFile:       t.currentFile,
		ProcessedBytes:    t.processed,
		TotalBytes:        t.total,
		Percent:           percent,
		SpeedMbps:         t.speedBps / (1024 * 1024),
		Logs:              logs,
		Error:             t.errText,
		Message:           t.message,
		SuggestedTapeName: t.suggested,
	}
}

// update применяет очередной ProgressUpdate; строки лога пишутся не
// чаще двух в секунду. Скорость — среднее по фазе от зафиксированной
// базы (processed0, t0): EWMA по мгновенным сэмплам на пиле
// «чанк из буфера ↔ ленточный I/O» сходился к геометрическому
// среднему всплеска и паузы и завышал скорость втрое (сессия 19).
func (t *Task) update(u port.ProgressUpdate, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != taskRunning {
		return
	}
	// База сбрасывается: на первом апдейте, при смене фазы и при
	// уменьшении processed (декодер считает байты локально для каждой
	// сессии — Full-restore читает сессии подряд).
	if t.baseSample.IsZero() || u.Phase != t.phase || u.ProcessedBytes < t.lastBytes {
		t.baseSample, t.baseBytes = now, u.ProcessedBytes
		t.speedBps = 0
	} else if dt := now.Sub(t.baseSample); dt > 0 {
		t.speedBps = float64(u.ProcessedBytes-t.baseBytes) / dt.Seconds()
	}
	t.lastBytes = u.ProcessedBytes
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
	if !t.finishableLocked() {
		t.mu.Unlock()
		return
	}
	t.state = taskSuccess
	t.finishedAt = now
	t.continueCh = nil
	t.appendLogLocked("задача завершена успешно")
	snapshot := t.finishSnapshotLocked()
	onFinish := t.onFinish
	t.mu.Unlock()
	if onFinish != nil {
		onFinish(snapshot)
	}
}

// finishError фиксирует завершение с ошибкой. Допустима и отмена
// в состоянии awaiting_tape: graceful shutdown отменяет ожидание
// кассеты, задача обязана завершиться, а не висеть до таймаута.
func (t *Task) finishError(err error, now time.Time) {
	t.mu.Lock()
	if !t.finishableLocked() {
		t.mu.Unlock()
		return
	}
	t.state = taskError
	t.finishedAt = now
	t.continueCh = nil
	t.errText = err.Error()
	t.appendLogLocked("задача завершена ошибкой: " + t.errText)
	snapshot := t.finishSnapshotLocked()
	onFinish := t.onFinish
	t.mu.Unlock()
	if onFinish != nil {
		onFinish(snapshot)
	}
}

func (t *Task) finishSnapshotLocked() TaskFinishSnapshot {
	tapes := append([]string{}, t.tapes...)
	bytes := t.bytes
	if bytes == 0 {
		// An error can happen after progress already counted a large transfer.
		bytes = t.processed
	}
	return TaskFinishSnapshot{
		Event: "task_finished", TaskID: t.ID, Kind: t.Kind, State: t.state,
		Error: t.errText, Job: t.job, Bytes: bytes, Files: t.files,
		Tapes: tapes, StartedAt: t.startedAt,
		FinishedAt: t.finishedAt, Version: t.version,
	}
}

// SetResult сохраняет крупинки результата до завершения задачи.
func (t *Task) SetResult(job string, bytes int64, files int, tapes []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if bytes == 0 {
		bytes = t.processed
	}
	t.job, t.bytes, t.files = job, bytes, files
	t.tapes = append([]string(nil), tapes...)
}

// finishableLocked — можно ли завершить задачу из текущего состояния.
// Вызывать под мьютексом.
func (t *Task) finishableLocked() bool {
	return t.state == taskRunning || t.state == taskAwaitingTape
}

// enterAwaiting переводит задачу в ожидание кассеты оператора:
// state=awaiting_tape, message и предложенное имя — в прогресс-объект.
// Возвращает канал ответа оператора; false — задача уже не бегущая
// (отменена), ожидание невозможно.
func (t *Task) enterAwaiting(message, suggested string) (chan string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != taskRunning {
		return nil, false
	}
	t.state = taskAwaitingTape
	t.message = message
	t.suggested = suggested
	t.continueCh = make(chan string, 1)
	t.appendLogLocked(message)
	return t.continueCh, true
}

// Continue доставляет ответ оператора задаче в awaiting_tape и
// возвращает фактическое имя кассеты (пустое — предложенное).
// Ошибка — задача не ожидает кассету (409 в REST).
func (t *Task) Continue(tapeName string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != taskAwaitingTape || t.continueCh == nil {
		return "", errors.New("задача не ожидает смену кассеты")
	}
	if tapeName == "" {
		tapeName = t.suggested
	}
	t.state = taskRunning
	t.suggested = ""
	t.appendLogLocked("оператор: продолжение на кассете " + tapeName)
	// Буфер 1 и единственный успешный Continue гарантируют место;
	// получатель мог уже уйти по отмене ctx — значение просто утонет.
	ch := t.continueCh
	t.continueCh = nil
	ch <- tapeName
	return tapeName, nil
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
	task      *Task
	clock     port.Clock
	deferDone bool
}

// NewTaskProgress создаёт репортёр прогресса для задачи.
func NewTaskProgress(task *Task, clock port.Clock) port.ProgressReporter {
	return &taskProgress{task: task, clock: clock}
}

// NewDeferredTaskProgress публикует прогресс, но оставляет успешную
// финализацию вызывающему коду. Это нужно, когда webhook должен включить
// Result use case, который вызывает Done до возврата Result.
func NewDeferredTaskProgress(task *Task, clock port.Clock) port.ProgressReporter {
	return &taskProgress{task: task, clock: clock, deferDone: true}
}

// Update публикует снимок прогресса.
func (p *taskProgress) Update(u port.ProgressUpdate) {
	p.task.update(u, p.clock.Now())
}

// Done фиксирует успех.
func (p *taskProgress) Done() {
	if p.deferDone {
		return
	}
	p.task.finishSuccess(p.clock.Now())
}

// Fail фиксирует ошибку.
func (p *taskProgress) Fail(err error) {
	p.task.finishError(err, p.clock.Now())
}

// TaskRegistry — общий реестр задач демона.
type TaskRegistry struct {
	mu       sync.Mutex
	tasks    map[string]*Task
	wg       sync.WaitGroup
	onFinish func(TaskFinishSnapshot)
	version  string
}

// NewTaskRegistry создаёт пустой реестр.
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{tasks: make(map[string]*Task)}
}

// NewTaskRegistryWithFinish создаёт реестр с callback завершения задач.
func NewTaskRegistryWithFinish(version string, onFinish func(TaskFinishSnapshot)) *TaskRegistry {
	return &TaskRegistry{tasks: make(map[string]*Task), version: version, onFinish: onFinish}
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
		onFinish:  r.onFinish,
		version:   r.version,
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

// hasActiveLocked — есть ли активная задача (running или awaiting_tape:
// ожидание кассеты держит стример так же, как запись). Вызывать под
// мьютексом.
func (r *TaskRegistry) hasActiveLocked() bool {
	for _, t := range r.tasks {
		if isActiveState(t.stateLocked()) {
			return true
		}
	}
	return false
}

// isActiveState — состояние, занимающее стример.
func isActiveState(state string) bool {
	return state == taskRunning || state == taskAwaitingTape
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

// HasActive сообщает, есть ли хоть одна активная задача (включая
// ожидание кассеты).
func (r *TaskRegistry) HasActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hasActiveLocked()
}

// Active возвращает активные задачи (включая awaiting_tape), старые
// первыми.
func (r *TaskRegistry) Active() []*Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var active []*Task
	for _, t := range r.tasks {
		if isActiveState(t.stateLocked()) {
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
