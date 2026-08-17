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

// Смена кассет в демоне (сессия 7 плана spanning): port.TapeChanger
// поверх реестра задач. RequestNext публикует в прогресс-объект
// state=awaiting_tape с текстом и предложенным именем кассеты и ждёт
// ответа оператора (POST /api/tasks/{id}/continue) либо отмены ctx
// (graceful shutdown закрывает ожидание). Затем changer сам
// переоткрывает устройство из настроек демона, форматирует кассету
// и регистрирует её в каталоге (бекап, протокол сессии 5) либо читает
// ярлык вставленной кассеты для сверки цепочки (restore, сессия 6).

package web

import (
	"context"
	"errors"
	"fmt"
	"io"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/format"
)

// daemonChanger — port.TapeChanger задач демона. cur — текущая лента
// задачи: меняется только горутиной задачи, финальное закрытие делает
// finish (промежуточные кассеты закрывает CloseTape сам).
type daemonChanger struct {
	srv  *Server
	task *Task
	job  string // задание бекапа (имя-фолбэк следующей кассеты)

	cur port.Tape
}

// CloseTape закрывает и извлекает кассету (Close + Eject/MTOFFL, как
// CLI-режим).
func (c *daemonChanger) CloseTape(ctx context.Context, tape port.Tape) error {
	if err := tape.Eject(ctx); err != nil {
		return fmt.Errorf("извлечение кассеты: %w", err)
	}
	if err := tape.Close(); err != nil {
		return fmt.Errorf("закрытие ленты: %w", err)
	}
	if c.cur == tape {
		c.cur = nil
	}
	return nil
}

// RequestNext приостанавливает задачу до ответа оператора, затем
// открывает устройство из настроек демона и подготавливает кассету:
// бекап (span/enospc) — форматирование именем оператора и регистрация
// в каталоге; restore — чтение ярлыка вставленной кассеты цепочки
// (без форматирования; имя и обратную ссылку сверит use case).
func (c *daemonChanger) RequestNext(
	ctx context.Context,
	req port.NextTapeRequest,
) (port.Tape, domain.TapeLabel, error) {
	suggested := req.NextTapeName
	if suggested == "" && req.Reason != port.ReasonRestore {
		suggested = domain.NextTapeName(req.JobName, req.FinishedTape)
	}
	ch, ok := c.task.enterAwaiting(awaitMessage(req, suggested), suggested)
	if !ok {
		return nil, domain.TapeLabel{}, errors.New("задача уже завершена — смена кассеты невозможна")
	}
	var name string
	select {
	case name = <-ch:
	case <-ctx.Done():
		return nil, domain.TapeLabel{}, fmt.Errorf(
			"ожидание кассеты %s отменено (остановка демона): %w", suggested, ctx.Err())
	}
	c.srv.deps.Log.Info("web: задача продолжена",
		"task", c.task.ID, "tape", name, "part", int(req.Part), "reason", req.Reason)

	tape, err := c.srv.openTape() // устройство из текущих настроек демона
	if err != nil {
		return nil, domain.TapeLabel{}, err
	}
	if req.Reason == port.ReasonRestore {
		label, err := readInsertedLabel(ctx, tape, c.srv.deps.Codec)
		if err != nil {
			if cerr := tape.Close(); cerr != nil {
				err = errors.Join(err, fmt.Errorf("закрытие ленты: %w", cerr))
			}
			return nil, domain.TapeLabel{}, err
		}
		c.cur = tape
		return tape, label, nil
	}
	label, err := format.New(tape, c.srv.deps.Codec, c.srv.deps.Catalog,
		c.srv.deps.Rand, c.srv.deps.Clock, c.srv.deps.Log).Format(ctx, name, false)
	if err != nil {
		var already *domain.AlreadyFormattedError
		if errors.As(err, &already) {
			err = fmt.Errorf(
				"вставленная кассета не чиста (%s); вставьте чистую и повторите продолжение: %w",
				already.Name, err)
		}
		if cerr := tape.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("закрытие ленты: %w", cerr))
		}
		return nil, domain.TapeLabel{}, fmt.Errorf("форматирование кассеты %s: %w", name, err)
	}
	c.cur = tape
	return tape, label, nil
}

// SuggestNextName предлагает имя следующей кассеты (инкремент
// числового суффикса текущей): указатель продолжения на текущей
// кассете пишется до паузы задачи.
func (c *daemonChanger) SuggestNextName(_ context.Context, current string) (string, error) {
	return domain.NextTapeName(c.job, current), nil
}

// finish закрывает текущую ленту задачи (финальную кассету цепочки;
// промежуточные закрыты CloseTape). Вызывается горутиной задачи.
func (c *daemonChanger) finish() {
	if c.cur == nil {
		return
	}
	if err := c.cur.Close(); err != nil {
		c.srv.deps.Log.Warn("web: закрытие ленты задачи", "error", err.Error())
	}
}

// awaitMessage — текст оператору в прогресс-объекте awaiting_tape.
func awaitMessage(req port.NextTapeRequest, suggested string) string {
	if req.Reason == port.ReasonRestore {
		return fmt.Sprintf("кассета %s прочитана: вставьте кассету %s (часть %d)",
			req.FinishedTape, suggested, req.Part)
	}
	return fmt.Sprintf("кассета %s закрыта (%s): вставьте чистую кассету %s (часть %d)",
		req.FinishedTape, req.Reason, suggested, req.Part)
}

// readInsertedLabel читает ярлык вставленной кассеты цепочки restore.
func readInsertedLabel(ctx context.Context, tape port.Tape, codec port.TapeCodec) (domain.TapeLabel, error) {
	if err := tape.Rewind(ctx); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("перемотка: %w", err)
	}
	block, err := tape.ReadBlock(ctx)
	if errors.Is(err, io.EOF) {
		return domain.TapeLabel{}, fmt.Errorf("чтение ярлыка: %w", &domain.BlankTapeError{})
	}
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("чтение ярлыка: %w", err)
	}
	label, err := codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("разбор ярлыка: %w", err)
	}
	return label, nil
}
