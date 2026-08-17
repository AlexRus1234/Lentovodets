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

// BackupUseCase — сканирование и запись сессии бекапа на ленту.
// См. docs/ARCHITECTURE.md §4.1, docs/SPECIFICATION.md §4.2.

package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/scan"
)

// Options — параметры запуска бекапа.
type Options struct {
	Full   bool // полный бекап: перезапись ленты с сессии 1
	DryRun bool // только сканирование, без записи и каталога
}

// Stats — статистика запуска.
type Stats struct {
	Scanned  int   // изменившихся путей (Added+Modified+Deleted)
	Added    int   // новых
	Modified int   // изменённых
	Deleted  int   // tombstone'ов (mirror)
	Bytes    int64 // байт к записи (без tombstone'ов)
}

// Result — итог запуска.
type Result struct {
	Session domain.Session // DryRun: нулевая сессия; иначе — сессия части 1
	Stats   Stats

	// PlannedParts — число частей сессии по планировщику spanning
	// (capacity/min_tail, план «тома»); 1 — одна кассета. Заполняется
	// и в DryRun.
	PlannedParts int

	// PlannedByBudget — часть 1 спланирована в остаток текущей кассеты
	// (дозапись), а не на полную ёмкость (FULL/первая сессия либо
	// остаток меньше min_tail — вся сессия на новую кассету).
	PlannedByBudget bool

	// Parts — фактически записанные части (успешный запуск пишет все
	// запланированные; ENOSPC-переносы меняют кассеты, не число частей).
	Parts int

	// Tapes — имена использованных кассет в порядке записи.
	Tapes []string
}

// UseCase выполняет бекап задания на ленту.
type UseCase struct {
	cfg     port.ConfigSource
	tape    port.Tape
	codec   port.TapeCodec
	cat     port.Catalog
	fs      port.Filesystem
	hasher  port.Hasher
	rand    port.Rand
	clock   port.Clock
	prog    port.ProgressReporter // nil допустим
	log     *slog.Logger
	changer port.TapeChanger // nil — смена кассет недоступна
}

// New собирает use case (wire в iface/cli). changer может быть nil:
// тогда планировщик, давший больше одной части, завершает запуск
// ошибкой *domain.TapeChangerError до записи.
func New(
	cfg port.ConfigSource,
	tape port.Tape,
	codec port.TapeCodec,
	cat port.Catalog,
	fs port.Filesystem,
	hasher port.Hasher,
	rand port.Rand,
	clock port.Clock,
	prog port.ProgressReporter,
	log *slog.Logger,
	changer port.TapeChanger,
) *UseCase {
	return &UseCase{
		cfg: cfg, tape: tape, codec: codec, cat: cat,
		fs: fs, hasher: hasher, rand: rand, clock: clock,
		prog: prog, log: log, changer: changer,
	}
}

// Backup сканирует ФС задания jobName и записывает сессию на ленту.
//
// Семантика (ARCHITECTURE §4.1, FORMAT §10, план «тома»):
//   - FULL (первая сессия на ленте или opts.Full): позиционирование
//     MTFSF(1), сессия 1; старые сессии ленты удаляются из каталога;
//   - INC: позиционирование MTFSF(2K+1) после K существующих сессий;
//   - снимок для сравнения — файлы последней сессии ленты;
//   - после скана сессия режется планировщиком частей по
//     capacity/min_tail (см. planSpanning); файл больше бюджета
//     кассеты — FileTooLargeError до записи (и в DryRun);
//   - частей больше одной — цикл spanning (writeParts): части на
//     разные кассеты через port.TapeChanger, указатели продолжения,
//     аварийный ENOSPC-перенос части; без changer'а —
//     *domain.TapeChangerError до записи;
//   - после записи сессии ставится замыкающая EOD-пара filemark'ов
//     (инвариант FORMAT §4: 2K+3 меток на ленте с K сессиями).
func (uc *UseCase) Backup(ctx context.Context, jobName string, opts Options) (Result, error) {
	st, err := uc.prepare(ctx, jobName)
	if err != nil {
		return Result{}, err
	}
	snapshot, err := uc.lastSnapshot(ctx, st.lastID)
	if err != nil {
		return Result{}, err
	}
	sc := scan.New(uc.fs, uc.hasher, uc.prog, uc.log)
	files, err := sc.Scan(ctx, st.job, snapshot)
	if err != nil {
		return Result{}, err
	}
	stats := statsOf(files)

	plan, err := uc.planSpanning(ctx, st.job.Name, opts, st.lastNum, st.sessions, files)
	if err != nil {
		return Result{}, err
	}
	if opts.DryRun {
		uc.done()
		return Result{
			Stats:           stats,
			PlannedParts:    len(plan.parts),
			PlannedByBudget: plan.byBudget,
		}, nil
	}

	if len(plan.parts) > 1 && uc.changer == nil {
		return Result{}, fmt.Errorf("backup: %w", &domain.TapeChangerError{})
	}
	if plan.newTape && uc.changer == nil {
		uc.log.Warn("смена кассеты недоступна (нет changer'а): пишем в текущую кассету с малым остатком")
	}
	isFull := opts.Full || st.lastNum == 0
	if isFull {
		if err := uc.dropSessions(ctx, st.sessions); err != nil {
			return Result{}, err
		}
	}
	return uc.writeParts(ctx, st, plan, isFull, stats)
}

// tapeState — состояние кассеты и задания, собранное до скана.
type tapeState struct {
	job      domain.Job
	label    domain.TapeLabel
	jobRunID string
	sessions []domain.Session
	lastNum  int32 // максимальный номер сессии ленты
	lastID   int64 // PK этой сессии; 0 — сессий нет
}

// prepare загружает задание, проверяет ярлык и кассету по каталогу,
// генерирует job_run_id и собирает сессии ленты.
func (uc *UseCase) prepare(ctx context.Context, jobName string) (tapeState, error) {
	job, err := uc.loadJob(jobName)
	if err != nil {
		return tapeState{}, err
	}
	label, err := uc.readLabel(ctx)
	if err != nil {
		return tapeState{}, err
	}
	if _, err := uc.cat.GetTapeByUUID(ctx, label.UUID); err != nil {
		return tapeState{}, fmt.Errorf(
			"backup: кассета %s неизвестна каталогу (сначала lentovodec tape format): %w",
			label.UUID, err)
	}
	jobRunID, err := uc.rand.UUID4()
	if err != nil {
		return tapeState{}, fmt.Errorf("backup: генерация job_run_id: %w", err)
	}
	sessions, err := uc.cat.ListSessions(ctx, label.UUID)
	if err != nil {
		return tapeState{}, fmt.Errorf("backup: список сессий кассеты %s: %w", label.UUID, err)
	}
	lastNum, lastID := lastSession(sessions)
	return tapeState{
		job:      job,
		label:    label,
		jobRunID: jobRunID,
		sessions: sessions,
		lastNum:  lastNum,
		lastID:   lastID,
	}, nil
}

// spanPlan — итог планирования сессии по ёмкости кассеты.
type spanPlan struct {
	parts    []domain.SpanPart // части в порядке сканера
	capacity int64             // ёмкость кассеты из конфига; 0 — spanning выключен
	budget   int64             // бюджет части 1; 0 — spanning выключен
	byBudget bool              // часть 1 спланирована в остаток кассеты
	newTape  bool              // остаток < min_tail: часть 1 на новую кассету
}

// planSpanning режет сессию на части после скана (план «тома» §2.2).
// Бюджет части 1: FULL/первая сессия — capacity целиком; дозапись —
// остаток capacity − Σ байт файлов сессий кассеты из каталога
// (tombstone'ы не считаются — на ленту не пишутся; индексный overhead
// не учитывается — оценка). Остаток меньше min_tail — бюджет capacity
// и признак newTape: вся сессия начинается на новой кассете через
// changer (writeParts). capacity = 0 — spanning выключен: одна
// часть без проверки размеров (переполнение ловит ENOSPC-путь записи).
// Ошибка планировщика (FileTooLargeError и сбой конфига/каталога)
// возвращается до каких-либо записей.
func (uc *UseCase) planSpanning(
	ctx context.Context,
	jobName string,
	opts Options,
	lastNum int32,
	sessions []domain.Session,
	files []domain.FileMeta,
) (spanPlan, error) {
	capacity, err := uc.cfg.Capacity()
	if err != nil {
		return spanPlan{}, fmt.Errorf("backup: чтение capacity: %w", err)
	}
	if capacity < 0 {
		return spanPlan{}, fmt.Errorf("backup: capacity = %d: отрицательная ёмкость", capacity)
	}
	var plan spanPlan
	plan.capacity = capacity
	if capacity > 0 {
		minTail, err := uc.cfg.MinTail()
		if err != nil {
			return spanPlan{}, fmt.Errorf("backup: чтение min_tail: %w", err)
		}
		if opts.Full || lastNum == 0 {
			plan.budget = capacity
		} else {
			used, err := uc.usedBytes(ctx, sessions)
			if err != nil {
				return spanPlan{}, err
			}
			rem := capacity - used
			if rem < 0 {
				rem = 0
			}
			if rem > 0 && rem >= minTail {
				plan.budget, plan.byBudget = rem, true
			} else {
				plan.budget, plan.newTape = capacity, true
			}
		}
	}
	plan.parts, err = domain.PlanSpan(files, plan.budget)
	if err != nil {
		return spanPlan{}, err
	}
	uc.log.Info("backup planned",
		slog.String("job", jobName),
		slog.Int("planned_parts", len(plan.parts)),
		slog.Int64("budget_bytes", plan.budget),
		slog.Bool("new_tape", plan.newTape))
	return plan, nil
}

// usedBytes — Σ байт файлов сессий кассеты из каталога (оценка
// занятого места для бюджета дозаписи). Tombstone'ы не считаются:
// на ленту не пишутся.
func (uc *UseCase) usedBytes(ctx context.Context, sessions []domain.Session) (int64, error) {
	var used int64
	for _, sess := range sessions {
		files, err := uc.cat.GetFilesBySession(ctx, sess.ID)
		if err != nil {
			return 0, fmt.Errorf("backup: файлы сессии %d для оценки остатка кассеты: %w", sess.ID, err)
		}
		for _, fm := range files {
			if !fm.IsDeleted() {
				used += fm.Size
			}
		}
	}
	return used, nil
}

// loadJob ищет задание по имени в конфигурации.
func (uc *UseCase) loadJob(jobName string) (domain.Job, error) {
	jobs, err := uc.cfg.Jobs()
	if err != nil {
		return domain.Job{}, fmt.Errorf("backup: чтение конфигурации: %w", err)
	}
	for _, j := range jobs {
		if j.Name == jobName {
			return j, nil
		}
	}
	return domain.Job{}, fmt.Errorf("backup: задание %q не найдено в конфигурации", jobName)
}

// readLabel читает ярлык с BOT; лента без ярлыка — BlankTapeError.
func (uc *UseCase) readLabel(ctx context.Context) (domain.TapeLabel, error) {
	if err := uc.tape.Rewind(ctx); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("backup: перемотка: %w", err)
	}
	block, err := uc.tape.ReadBlock(ctx)
	if errors.Is(err, io.EOF) {
		return domain.TapeLabel{}, fmt.Errorf("backup: %w", &domain.BlankTapeError{})
	}
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("backup: чтение ярлыка: %w", err)
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("backup: разбор ярлыка: %w", err)
	}
	return label, nil
}

// lastSnapshot возвращает файлы сессии lastID (nil для FULL-рестарта
// и пустой ленты).
func (uc *UseCase) lastSnapshot(ctx context.Context, lastID int64) ([]domain.FileMeta, error) {
	if lastID == 0 {
		return nil, nil
	}
	files, err := uc.cat.GetFilesBySession(ctx, lastID)
	if err != nil {
		return nil, fmt.Errorf("backup: файлы прошлой сессии %d: %w", lastID, err)
	}
	return files, nil
}

// writePart позиционирует ленту, пишет часть сессии (индекс + tar +
// блок-указатель продолжения) и закрывает EOD-парой. lastNum — число
// сессий, остающихся на ленте до записываемой части (0 для новой
// кассеты); continues — UUID кассеты предыдущей части (Continues
// индекса, "" у части 1); cont — указатель продолжения (nil у
// заключительной части: пара EOD без блока). Сбой любой стадии
// откатывает сессию из каталога и восстанавливает старый EOD
// (сессия 1 плана spanning).
func (uc *UseCase) writePart(
	ctx context.Context,
	job domain.Job,
	tape port.Tape,
	sess domain.Session,
	files []domain.FileMeta,
	lastNum int32,
	continues string,
	cont *port.Continuation,
) error {
	if err := tape.Rewind(ctx); err != nil {
		return fmt.Errorf("backup: перемотка перед записью: %w", err)
	}
	if err := tape.ForwardFilemarks(ctx, int(2*lastNum+1)); err != nil {
		return fmt.Errorf("backup: позиционирование на сессию %d: %w", sess.Num, err)
	}
	tracker := &writeTracker{ProgressReporter: uc.prog}
	header := port.SessionHeader{
		SessionNum: sess.Num,
		Type:       sess.Type,
		JobRunID:   sess.JobRunID,
		Timestamp:  sess.Timestamp,
		JobName:    job.Name,
		Part:       sess.Part,
		Continues:  continues,
	}
	writeErr := uc.codec.WriteSession(ctx, tape, header, files, uc.fs, tracker)
	if writeErr == nil && cont != nil {
		writeErr = uc.codec.WriteContinuation(ctx, tape, *cont)
	}
	if writeErr == nil {
		writeErr = uc.closeEOD(ctx, tape)
	}
	if writeErr != nil {
		if delErr := uc.cat.DeleteSession(ctx, sess.ID); delErr != nil {
			writeErr = errors.Join(writeErr,
				fmt.Errorf("backup: откат сессии %d из каталога: %w", sess.ID, delErr))
		}
		writeErr = errors.Join(writeErr, uc.restoreEOD(ctx, tape, lastNum))
		return mapTapeFull(writeErr, tracker.written)
	}
	return nil
}

// restoreEOD — best-effort восстановление ленты после сбоя записи
// сессии: за старым EOD может остаться грязный хвост частичной записи,
// делающий дозапись невозможной. Позиция старого EOD вычисляется из
// инварианта FORMAT §4 (лента с lastNum сессиями содержит 2*lastNum+1
// меток ярлыка и сессий до закрывающей пары), запись пары WriteEOF с
// этой позиции усекает хвост (запись с позиции уничтожает остаток
// ленты). Новых операций port.Tape не требуется. Ошибки восстановления
// не глотаются и несут рекомендацию оператору; сбой перемотки
// присоединяется как есть — привести ленту нечем.
func (uc *UseCase) restoreEOD(ctx context.Context, tape port.Tape, lastNum int32) error {
	const advice = "кассета читается, для дозаписи выполните lentovodec tape readtest; при повторных сбоях — переформатировать"
	if err := tape.Rewind(ctx); err != nil {
		return fmt.Errorf("backup: восстановление ленты после сбоя записи: перемотка: %w", err)
	}
	if err := tape.ForwardFilemarks(ctx, int(2*lastNum+1)); err != nil {
		return fmt.Errorf(
			"backup: восстановление ленты после сбоя записи: позиционирование MTFSF(%d): %w; %s",
			2*lastNum+1, err, advice)
	}
	if err := uc.closeEOD(ctx, tape); err != nil {
		return fmt.Errorf("backup: восстановление ленты после сбоя записи: запись EOD-пары: %w; %s", err, advice)
	}
	return nil
}

// closeEOD ставит замыкающую пару filemark'ов (FORMAT §4).
func (uc *UseCase) closeEOD(ctx context.Context, tape port.Tape) error {
	if err := tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("backup: filemark EOD #1: %w", err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("backup: filemark EOD #2: %w", err)
	}
	return nil
}

// dropSessions удаляет сессии ленты из каталога (FULL-рестарт).
func (uc *UseCase) dropSessions(ctx context.Context, sessions []domain.Session) error {
	for _, sess := range sessions {
		if err := uc.cat.DeleteSession(ctx, sess.ID); err != nil {
			return fmt.Errorf("backup: очистка сессии %d перед FULL: %w", sess.ID, err)
		}
	}
	return nil
}

// progUpdate публикует обновление прогресса, если репортёр задан.
func (uc *UseCase) progUpdate(u port.ProgressUpdate) {
	if uc.prog != nil {
		uc.prog.Update(u)
	}
}

// done фиксирует успешное завершение.
func (uc *UseCase) done() {
	if uc.prog != nil {
		uc.prog.Done()
	}
}

// fail фиксирует сбой.
func (uc *UseCase) fail(err error) {
	if uc.prog != nil {
		uc.prog.Fail(err)
	}
}

// lastSession возвращает максимальный номер сессии и её PK.
func lastSession(sessions []domain.Session) (num int32, id int64) {
	for _, sess := range sessions {
		if sess.Num > num {
			num, id = sess.Num, sess.ID
		}
	}
	return num, id
}

// statsOf считает статистику по файлам сессии.
func statsOf(files []domain.FileMeta) Stats {
	var st Stats
	for _, fm := range files {
		st.Scanned++
		switch {
		case fm.IsAdded():
			st.Added++
		case fm.IsDeleted():
			st.Deleted++
		default:
			st.Modified++
		}
		if !fm.IsDeleted() {
			st.Bytes += fm.Size
		}
	}
	return st
}

// mapTapeFull превращает ошибки записи, означающие конец ленты, в
// *domain.TapeFullError с заполненным Written; прочие ошибки — как есть.
func mapTapeFull(err error, written int64) error {
	var full *domain.TapeFullError
	if errors.As(err, &full) {
		if full.Written == 0 {
			full.Written = written
		}
		return full
	}
	if strings.Contains(err.Error(), "no space left on device") {
		return errors.Join(&domain.TapeFullError{Written: written}, err)
	}
	return err
}

// writeTracker следит за числом записанных байт (фаза write) и
// пробрасывает обновления дальше. Done/Fail вызывающий публикует сам
// (контракт port.ProgressReporter у кодека — только Update).
type writeTracker struct {
	port.ProgressReporter // может быть nil
	written               int64
}

// Update запоминает максимум ProcessedBytes фазы write.
func (t *writeTracker) Update(u port.ProgressUpdate) {
	if u.Phase == port.PhaseWrite && u.ProcessedBytes > t.written {
		t.written = u.ProcessedBytes
	}
	if t.ProgressReporter != nil {
		t.ProgressReporter.Update(u)
	}
}
