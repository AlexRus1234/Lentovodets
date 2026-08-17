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

// Цикл spanning: части сессии на разные кассеты, указатели
// продолжения, аварийный ENOSPC-перенос. План «тома» §2, сессия 5.

package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// spanTape — текущая кассета цикла частей: открытая лента, её ярлык,
// число сессий до записываемой части и UUID кассеты предыдущей части
// (обратная ссылка Continues индекса, "" у части 1).
type spanTape struct {
	tape      port.Tape
	label     domain.TapeLabel
	lastNum   int32
	continues string
}

// spanRun — состояние цикла частей одного запуска.
type spanRun struct {
	uc     *UseCase
	st     tapeState
	plan   spanPlan
	isFull bool
	ctx    context.Context

	ts                 spanTape
	budget             int64 // бюджет части на текущей кассете
	prevMoveAtCapacity bool  // предыдущий ENOSPC-перенос был с бюджетом capacity

	first    domain.Session // сессия части 1 (для Result)
	firstSet bool
	tapes    []string // имена использованных кассет по порядку
	created  []int64  // зафиксированные в каталоге сессии этого запуска
}

// writeParts записывает части сессии по кассетам и возвращает итог.
// Вызывается после скана, планирования и (для FULL) очистки каталога;
// при частях >1 или newTape changer обязан быть не nil (проверено
// вызывающим, кроме деградации newTape без changer'а — тогда пишем
// в текущую кассету).
func (uc *UseCase) writeParts(ctx context.Context, st tapeState, plan spanPlan, isFull bool, stats Stats) (Result, error) {
	r := &spanRun{
		uc: uc, st: st, plan: plan, isFull: isFull, ctx: ctx,
		ts:     spanTape{tape: uc.tape, label: st.label, lastNum: st.lastNum},
		budget: plan.budget,
	}
	if err := r.run(ctx); err != nil {
		uc.fail(err)
		return Result{}, err
	}
	uc.done()
	uc.log.Info("backup finished",
		slog.String("job", st.job.Name),
		slog.Int64("session_id", r.first.ID),
		slog.Int("session_num", int(r.first.Num)),
		slog.String("type", string(r.first.Type)),
		slog.Int("parts", len(plan.parts)),
		slog.String("tapes", strings.Join(r.tapes, ",")),
		slog.Int("added", stats.Added),
		slog.Int("modified", stats.Modified),
		slog.Int("deleted", stats.Deleted),
		slog.Int64("bytes", stats.Bytes))
	return Result{
		Session:         r.first,
		Stats:           stats,
		PlannedParts:    len(plan.parts),
		PlannedByBudget: plan.byBudget,
		Parts:           len(plan.parts),
		Tapes:           r.tapes,
	}, nil
}

// run выполняет цикл частей: min_tail-переход на новую кассету, затем
// части по порядку с плановыми сменами и аварийными ENOSPC-переносами.
func (r *spanRun) run(ctx context.Context) error {
	if r.plan.newTape && r.uc.changer != nil {
		if err := r.rotate(ctx, 1, "остаток кассеты меньше min_tail"); err != nil {
			return err
		}
	}
	for k := 0; k < len(r.plan.parts); k++ {
		sess, cont, err := r.beginPart(ctx, k)
		if err != nil {
			return err
		}
		werr := r.uc.writePart(ctx, r.st.job, r.ts.tape, sess, r.plan.parts[k],
			r.ts.lastNum, r.ts.continues, cont)
		if werr == nil {
			if err := r.commit(ctx, k, sess); err != nil {
				return err
			}
			continue
		}
		if !r.canMoveAfterFull(werr) {
			return werr
		}
		if err := r.moveAfterENOSPC(ctx, k, sess); err != nil {
			return errors.Join(werr, err)
		}
		k-- // часть уходит на новую кассету целиком — повторяем тот же k
	}
	return nil
}

// beginPart строит сессию части k и указатель продолжения (nil у
// заключительной части) и регистрирует сессию в каталоге.
func (r *spanRun) beginPart(ctx context.Context, k int) (domain.Session, *port.Continuation, error) {
	sess := domain.Session{
		TapeUUID:  r.ts.label.UUID,
		Num:       r.ts.lastNum + 1,
		Type:      domain.SessionInc,
		Timestamp: r.uc.clock.Now().Unix(),
		JobRunID:  r.st.jobRunID,
		Part:      int32(k + 1),
	}
	// часть 1 наследует тип запуска (FULL-рестарт или INC-дозапись);
	// на свежей кассете (после min_tail-смены или для частей ≥2)
	// сессия 1 и тип FULL (правило каталога, сессия 4 плана).
	if r.isFull || r.ts.lastNum == 0 {
		sess.Num, sess.Type = 1, domain.SessionFull
	}
	var cont *port.Continuation
	if k+1 < len(r.plan.parts) {
		// имя следующей кассеты решено до записи: указатель пишется
		// в конце этой части (UUID следующей неизвестен до форматирования)
		next, err := r.uc.changer.SuggestNextName(ctx, r.ts.label.Name)
		if err != nil {
			return domain.Session{}, nil, r.rollbackRun(fmt.Errorf("backup: имя следующей кассеты: %w", err))
		}
		cont = &port.Continuation{
			JobRunID:     r.st.jobRunID,
			SessionNum:   sess.Num,
			Part:         int32(k + 2),
			NextTapeName: next,
		}
	}
	var err error
	sess.ID, err = r.uc.cat.CreateSession(ctx, sess)
	if err != nil {
		return domain.Session{}, nil, fmt.Errorf("backup: создание сессии: %w", err)
	}
	return sess, cont, nil
}

// commit фиксирует записанную часть в каталоге и бухгалтерии цикла;
// для незаключительной части — плановая смена кассеты.
func (r *spanRun) commit(ctx context.Context, k int, sess domain.Session) error {
	r.uc.progUpdate(port.ProgressUpdate{Phase: port.PhaseFinalize})
	if err := r.uc.cat.SaveFiles(ctx, sess.ID, r.plan.parts[k]); err != nil {
		return fmt.Errorf("backup: сохранение файлов сессии %d: %w", sess.ID, err)
	}
	r.ts.lastNum = sess.Num
	r.created = append(r.created, sess.ID)
	if !r.firstSet {
		r.first, r.firstSet = sess, true
	}
	r.tapes = append(r.tapes, r.ts.label.Name)
	r.prevMoveAtCapacity = false
	if k+1 < len(r.plan.parts) {
		return r.rotate(ctx, int32(k+2), "плановое продолжение цепочки")
	}
	return nil
}

// rotate выполняет плановую смену кассеты: закрывает текущую
// (указатель продолжения уже на ней) и получает следующую через changer.
// Сбой смены откатывает сессии запуска из каталога.
func (r *spanRun) rotate(ctx context.Context, part int32, why string) error {
	next, err := r.uc.changer.SuggestNextName(ctx, r.ts.label.Name)
	if err != nil {
		return r.rollbackRun(fmt.Errorf("backup: имя следующей кассеты: %w", err))
	}
	r.uc.tapeChanged(r.ts.label.Name, part, next, why)
	if err := r.swap(ctx, part, next, port.ReasonSpan); err != nil {
		return r.rollbackRun(err)
	}
	return nil
}

// swap закрывает текущую кассету и открывает следующую через changer.
func (r *spanRun) swap(ctx context.Context, part int32, next, reason string) error {
	if err := r.uc.changer.CloseTape(ctx, r.ts.tape); err != nil {
		return fmt.Errorf("backup: закрытие кассеты %s: %w", r.ts.label.Name, err)
	}
	tape, label, err := r.uc.changer.RequestNext(ctx, port.NextTapeRequest{
		JobName:      r.st.job.Name,
		FinishedTape: r.ts.label.Name,
		NextTapeName: next,
		Part:         part,
		Reason:       reason,
	})
	if err != nil {
		return fmt.Errorf("backup: получение следующей кассеты: %w", err)
	}
	r.ts = spanTape{tape: tape, label: label, continues: r.ts.label.UUID}
	r.budget = r.plan.capacity
	return nil
}

// canMoveAfterFull сообщает, допустим ли ENOSPC-перенос части на
// следующую кассету: ошибка обязана быть TapeFullError, spanning
// включён (capacity > 0) и changer доступен.
func (r *spanRun) canMoveAfterFull(err error) bool {
	var full *domain.TapeFullError
	if !errors.As(err, &full) {
		return false
	}
	return r.uc.changer != nil && r.plan.capacity > 0
}

// moveAfterENOSPC переносит не влезшую часть на следующую кассету:
// откат уже сделан writePart'ом (сессия 1 плана), здесь дописывается
// указатель продолжения на заполненную кассету и меняется лента.
// Два подряд переноса с бюджетом capacity — оценка ёмкости промахнулась,
// продолжать бессмысленно. Сбой переноса откатывает сессии запуска.
func (r *spanRun) moveAfterENOSPC(ctx context.Context, k int, sess domain.Session) error {
	moveAtCapacity := r.budget == r.plan.capacity
	if moveAtCapacity && r.prevMoveAtCapacity {
		return fmt.Errorf(
			"backup: часть не влезает в целую кассету (capacity = %d байт): уменьшите capacity в конфиге",
			r.plan.capacity)
	}
	r.prevMoveAtCapacity = moveAtCapacity
	next, err := r.uc.changer.SuggestNextName(ctx, r.ts.label.Name)
	if err != nil {
		return r.rollbackRun(fmt.Errorf("backup: перенос части %d: имя кассеты: %w", k+1, err))
	}
	if err := r.uc.abandonTape(ctx, r.ts, r.st.jobRunID, sess.Num, int32(k+1), next); err != nil {
		return r.rollbackRun(fmt.Errorf("backup: перенос части %d: %w", k+1, err))
	}
	r.uc.tapeChanged(r.ts.label.Name, int32(k+1), next, "кассета заполнена")
	if err := r.swap(ctx, int32(k+1), next, port.ReasonEnospc); err != nil {
		return r.rollbackRun(fmt.Errorf("backup: перенос части %d: %w", k+1, err))
	}
	return nil
}

// rollbackRun откатывает зафиксированные сессии запуска из каталога:
// смена кассеты не состоялась, цепочка не собрана — запуск атомарно
// не завершён. Откат идёт в контексте без отмены (сам ctx может быть
// уже закрыт); данные на лентах остаются, повторный запуск перепишет
// указатели продолжения.
func (r *spanRun) rollbackRun(cause error) error {
	ctx := context.WithoutCancel(r.ctx)
	for _, id := range r.created {
		if err := r.uc.cat.DeleteSession(ctx, id); err != nil {
			cause = errors.Join(cause, fmt.Errorf("backup: откат сессии %d из каталога: %w", id, err))
		}
	}
	return cause
}

// abandonTape закрывает заполненную кассету указателем продолжения:
// позиция старого EOD (инвариант FORMAT §4), блок-указатель, новая
// EOD-пара. Без указателя цепочка кассет теряла бы связность для
// последовательного чтения (сессия 6).
func (uc *UseCase) abandonTape(
	ctx context.Context,
	ts spanTape,
	jobRunID string,
	sessionNum, part int32,
	nextName string,
) error {
	if err := ts.tape.Rewind(ctx); err != nil {
		return fmt.Errorf("перемотка перед указателем продолжения: %w", err)
	}
	if err := ts.tape.ForwardFilemarks(ctx, int(2*ts.lastNum+1)); err != nil {
		return fmt.Errorf("позиционирование для указателя продолжения: %w", err)
	}
	if err := uc.codec.WriteContinuation(ctx, ts.tape, port.Continuation{
		JobRunID:     jobRunID,
		SessionNum:   sessionNum,
		Part:         part,
		NextTapeName: nextName,
	}); err != nil {
		return fmt.Errorf("запись указателя продолжения: %w", err)
	}
	return uc.closeEOD(ctx, ts.tape)
}

// tapeChanged публикует прогресс смены кассеты (текст — оператору).
func (uc *UseCase) tapeChanged(finished string, part int32, next, reason string) {
	uc.progUpdate(port.ProgressUpdate{
		Phase: port.PhaseTapeChange,
		Message: fmt.Sprintf("кассета %s закрыта (%s): вставьте чистую кассету, часть %d будет записана на %s",
			finished, reason, part, next),
	})
}
