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

// Следование цепочке кассет при последовательном чтении (Full-restore
// и ReadTest). План «тома» §4, сессия 6: цепочка читается с лент, не
// из каталога (DR-семантика); каталог — только best-effort отчёт.

package restore

import (
	"context"
	"fmt"
	"log/slog"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// TapeState — текущая кассета цепочки: открытая лента и её ярлык.
type TapeState struct {
	Tape  port.Tape
	Label domain.TapeLabel
}

// ChainFollower выполняет смену кассеты по указателю продолжения.
// Общий помощник RestoreFull и ReadTest: закрывает прочитанную
// кассету, получает следующую через port.TapeChanger (Reason=
// ReasonRestore) и сверяет её с цепочкой — имя ярлыка обязано
// совпасть с ожидаемым из указателя, а индекс первой сессии новой
// кассеты — нести обратную ссылку continues == UUID предыдущей.
// Несовпадение — *domain.ChainMismatchError: сразу ошибка, повторный
// запуск после вставки верной кассеты восстановит пропущенное.
type ChainFollower struct {
	Changer port.TapeChanger      // смена кассет; не nil при использовании
	Codec   port.TapeCodec        // чтение заголовка сессии 1
	Cat     port.Catalog          // nil допустим: каталог не обязателен (DR)
	Prog    port.ProgressReporter // nil допустим
	Log     *slog.Logger
}

// Follow меняет кассету по указателю продолжения cont и возвращает
// новую кассету, позиционированную на индекс сессии 1 (Rewind +
// MTFSF(1)) — последовательное чтение продолжается с неё.
func (f *ChainFollower) Follow(
	ctx context.Context,
	cur TapeState,
	cont *domain.ContinuationError,
) (TapeState, error) {
	f.progress(port.ProgressUpdate{
		Phase: port.PhaseTapeChange,
		Message: fmt.Sprintf("кассета %s закрыта: вставьте кассету %s, часть %d",
			cur.Label.Name, cont.NextTapeName, cont.Part),
	})
	if err := f.Changer.CloseTape(ctx, cur.Tape); err != nil {
		return TapeState{}, fmt.Errorf("закрытие кассеты %s: %w", cur.Label.Name, err)
	}
	tape, label, err := f.Changer.RequestNext(ctx, port.NextTapeRequest{
		FinishedTape: cur.Label.Name,
		NextTapeName: cont.NextTapeName,
		Part:         cont.Part,
		Reason:       port.ReasonRestore,
	})
	if err != nil {
		return TapeState{}, fmt.Errorf(
			"получение кассеты %s (часть %d): %w", cont.NextTapeName, cont.Part, err)
	}
	if label.Name != cont.NextTapeName {
		return TapeState{}, fmt.Errorf("имя ярлыка вставленной кассеты: %w",
			&domain.ChainMismatchError{Expected: cont.NextTapeName, Got: label.Name})
	}
	next, err := f.verifyContinues(ctx, tape, label, cur.Label.UUID)
	if err != nil {
		return TapeState{}, err
	}
	f.logSwap(ctx, cur.Label.Name, next.Label, cont)
	return next, nil
}

// verifyContinues сверяет обратную ссылку: индекс сессии 1 новой
// кассеты обязан нести continues == UUID предыдущей. Позиция после
// сверки возвращается к индексу сессии 1 для последовательного чтения.
func (f *ChainFollower) verifyContinues(
	ctx context.Context,
	tape port.Tape,
	label domain.TapeLabel,
	prevUUID string,
) (TapeState, error) {
	if err := tape.Rewind(ctx); err != nil {
		return TapeState{}, fmt.Errorf("перемотка кассеты %s: %w", label.Name, err)
	}
	if err := tape.ForwardFilemarks(ctx, 1); err != nil {
		return TapeState{}, fmt.Errorf("пропуск ярлыка кассеты %s: %w", label.Name, err)
	}
	header, err := f.Codec.ReadHeader(ctx, tape)
	if err != nil {
		return TapeState{}, fmt.Errorf("чтение индекса кассеты %s: %w", label.Name, err)
	}
	if header.Continues != prevUUID {
		return TapeState{}, fmt.Errorf("обратная ссылка continues сессии 1 кассеты %s: %w",
			label.Name, &domain.ChainMismatchError{Expected: prevUUID, Got: header.Continues})
	}
	// позиция — за filemark'ом индекса сессии 1; чтение цепочки снова
	// начинается с сессии 1 новой кассеты
	if err := tape.Rewind(ctx); err != nil {
		return TapeState{}, fmt.Errorf("перемотка кассеты %s: %w", label.Name, err)
	}
	if err := tape.ForwardFilemarks(ctx, 1); err != nil {
		return TapeState{}, fmt.Errorf("пропуск ярлыка кассеты %s: %w", label.Name, err)
	}
	return TapeState{Tape: tape, Label: label}, nil
}

// logSwap фиксирует смену; каталог, знающий запуск, добавляет отчёт
// о полной длине цепочки (best-effort, данные только с лент).
func (f *ChainFollower) logSwap(ctx context.Context, from string, to domain.TapeLabel, cont *domain.ContinuationError) {
	attrs := []any{
		slog.String("from", from),
		slog.String("to", to.Name),
		slog.Int("part", int(cont.Part)),
		slog.String("job_run_id", cont.JobRunID),
	}
	if f.Cat != nil {
		if chain, err := f.Cat.GetSessionChain(ctx, cont.JobRunID); err != nil {
			f.Log.Warn("цепочка: каталог недоступен для отчёта", slog.String("error", err.Error()))
		} else if len(chain) > 0 {
			attrs = append(attrs, slog.Int("parts_known", len(chain)))
		}
	}
	f.Log.Info("цепочка: кассета сменена", attrs...)
}

// progress публикует обновление, если репортёр задан.
func (f *ChainFollower) progress(u port.ProgressUpdate) {
	if f.Prog != nil {
		f.Prog.Update(u)
	}
}
