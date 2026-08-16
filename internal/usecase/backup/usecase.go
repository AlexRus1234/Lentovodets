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
	Session domain.Session // DryRun: нулевая сессия
	Stats   Stats
}

// UseCase выполняет бекап задания на ленту.
type UseCase struct {
	cfg    port.ConfigSource
	tape   port.Tape
	codec  port.TapeCodec
	cat    port.Catalog
	fs     port.Filesystem
	hasher port.Hasher
	rand   port.Rand
	clock  port.Clock
	prog   port.ProgressReporter // nil допустим
	log    *slog.Logger
}

// New собирает use case (wire в iface/cli).
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
) *UseCase {
	return &UseCase{
		cfg: cfg, tape: tape, codec: codec, cat: cat,
		fs: fs, hasher: hasher, rand: rand, clock: clock,
		prog: prog, log: log,
	}
}

// Backup сканирует ФС задания jobName и записывает сессию на ленту.
//
// Семантика (ARCHITECTURE §4.1, FORMAT §10):
//   - FULL (первая сессия на ленте или opts.Full): позиционирование
//     MTFSF(1), сессия 1; старые сессии ленты удаляются из каталога;
//   - INC: позиционирование MTFSF(2K+1) после K существующих сессий;
//   - снимок для сравнения — файлы последней сессии ленты;
//   - после записи сессии ставится замыкающая EOD-пара filemark'ов
//     (инвариант FORMAT §4: 2K+3 меток на ленте с K сессиями).
func (uc *UseCase) Backup(ctx context.Context, jobName string, opts Options) (Result, error) {
	job, err := uc.loadJob(jobName)
	if err != nil {
		return Result{}, err
	}
	label, err := uc.readLabel(ctx)
	if err != nil {
		return Result{}, err
	}
	if _, err := uc.cat.GetTapeByUUID(ctx, label.UUID); err != nil {
		return Result{}, fmt.Errorf(
			"backup: кассета %s неизвестна каталогу (сначала lentovodec tape format): %w",
			label.UUID, err)
	}
	jobRunID, err := uc.rand.UUID4()
	if err != nil {
		return Result{}, fmt.Errorf("backup: генерация job_run_id: %w", err)
	}
	sessions, err := uc.cat.ListSessions(ctx, label.UUID)
	if err != nil {
		return Result{}, fmt.Errorf("backup: список сессий кассеты %s: %w", label.UUID, err)
	}
	lastNum, lastID := lastSession(sessions)

	snapshot, err := uc.lastSnapshot(ctx, lastID)
	if err != nil {
		return Result{}, err
	}
	sc := scan.New(uc.fs, uc.hasher, uc.prog, uc.log)
	files, err := sc.Scan(ctx, job, snapshot)
	if err != nil {
		return Result{}, err
	}
	stats := statsOf(files)
	if opts.DryRun {
		uc.done()
		return Result{Stats: stats}, nil
	}

	isFull := opts.Full || lastNum == 0
	sess := domain.Session{
		TapeUUID:  label.UUID,
		Num:       lastNum + 1,
		Type:      domain.SessionInc,
		Timestamp: uc.clock.Now().Unix(),
		JobRunID:  jobRunID,
	}
	if isFull {
		sess.Num, sess.Type = 1, domain.SessionFull
		if err := uc.dropSessions(ctx, sessions); err != nil {
			return Result{}, err
		}
	}
	sess.ID, err = uc.cat.CreateSession(ctx, sess)
	if err != nil {
		return Result{}, fmt.Errorf("backup: создание сессии: %w", err)
	}

	if err := uc.writeSession(ctx, job, sess, files, lastNum); err != nil {
		uc.fail(err)
		return Result{}, err
	}
	uc.progUpdate(port.ProgressUpdate{Phase: port.PhaseFinalize})
	if err := uc.cat.SaveFiles(ctx, sess.ID, files); err != nil {
		return Result{}, fmt.Errorf("backup: сохранение файлов сессии %d: %w", sess.ID, err)
	}
	uc.done()
	uc.log.Info("backup finished",
		slog.String("job", job.Name),
		slog.Int64("session_id", sess.ID),
		slog.Int("session_num", int(sess.Num)),
		slog.String("type", string(sess.Type)),
		slog.Int("added", stats.Added),
		slog.Int("modified", stats.Modified),
		slog.Int("deleted", stats.Deleted),
		slog.Int64("bytes", stats.Bytes))
	return Result{Session: sess, Stats: stats}, nil
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

// writeSession позиционирует ленту, пишет сессию и закрывает EOD-пару.
// lastNum — число сессий, остающихся на ленте (0 для FULL-рестарта).
func (uc *UseCase) writeSession(
	ctx context.Context,
	job domain.Job,
	sess domain.Session,
	files []domain.FileMeta,
	lastNum int32,
) error {
	if err := uc.tape.Rewind(ctx); err != nil {
		return fmt.Errorf("backup: перемотка перед записью: %w", err)
	}
	if err := uc.tape.ForwardFilemarks(ctx, int(2*lastNum+1)); err != nil {
		return fmt.Errorf("backup: позиционирование на сессию %d: %w", sess.Num, err)
	}
	tracker := &writeTracker{ProgressReporter: uc.prog}
	header := port.SessionHeader{
		SessionNum: sess.Num,
		Type:       sess.Type,
		JobRunID:   sess.JobRunID,
		Timestamp:  sess.Timestamp,
		JobName:    job.Name,
	}
	writeErr := uc.codec.WriteSession(ctx, uc.tape, header, files, uc.fs, tracker)
	if writeErr == nil {
		writeErr = uc.closeEOD(ctx)
	}
	if writeErr != nil {
		if delErr := uc.cat.DeleteSession(ctx, sess.ID); delErr != nil {
			writeErr = errors.Join(writeErr,
				fmt.Errorf("backup: откат сессии %d из каталога: %w", sess.ID, delErr))
		}
		writeErr = errors.Join(writeErr, uc.restoreEOD(ctx, lastNum))
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
func (uc *UseCase) restoreEOD(ctx context.Context, lastNum int32) error {
	const advice = "кассета читается, для дозаписи выполните lentovodec tape readtest; при повторных сбоях — переформатировать"
	if err := uc.tape.Rewind(ctx); err != nil {
		return fmt.Errorf("backup: восстановление ленты после сбоя записи: перемотка: %w", err)
	}
	if err := uc.tape.ForwardFilemarks(ctx, int(2*lastNum+1)); err != nil {
		return fmt.Errorf(
			"backup: восстановление ленты после сбоя записи: позиционирование MTFSF(%d): %w; %s",
			2*lastNum+1, err, advice)
	}
	if err := uc.closeEOD(ctx); err != nil {
		return fmt.Errorf("backup: восстановление ленты после сбоя записи: запись EOD-пары: %w; %s", err, advice)
	}
	return nil
}

// closeEOD ставит замыкающую пару filemark'ов (FORMAT §4).
func (uc *UseCase) closeEOD(ctx context.Context) error {
	if err := uc.tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("backup: filemark EOD #1: %w", err)
	}
	if err := uc.tape.WriteEOF(ctx); err != nil {
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
