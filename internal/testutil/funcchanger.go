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

// FuncChanger — двойник port.TapeChanger поверх замыканий: сценарии
// смены кассет (авто-фабрики filetape/FakeTape) собираются в самих
// тестах. См. docs/TESTING.md §3.

package testutil

import (
	"context"
	"fmt"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// FuncChanger делегирует смену кассет замыканиям; незаданное замыкание
// заменяется разумным умолчанием (Suggest — domain.NextTapeName,
// Close — tape.Close, Request — ошибка). Все запросы запоминаются.
type FuncChanger struct {
	// Suggest подбирает имя следующей кассеты; nil — инкремент суффикса
	// текущего имени (domain.NextTapeName с пустым заданием).
	Suggest func(ctx context.Context, current string) (string, error)

	// Close закрывает текущую ленту; nil — tape.Close.
	Close func(ctx context.Context, tape port.Tape) error

	// Request выдаёт следующую кассету; nil — ошибка «нет кассет».
	Request func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error)

	Requests []port.NextTapeRequest // все RequestNext-запросы по порядку
	Closed   int                    // число CloseTape-вызовов
}

// SuggestNextName подбирает имя следующей кассеты.
func (c *FuncChanger) SuggestNextName(ctx context.Context, current string) (string, error) {
	if c.Suggest != nil {
		return c.Suggest(ctx, current)
	}
	return domain.NextTapeName("", current), nil
}

// CloseTape закрывает ленту.
func (c *FuncChanger) CloseTape(ctx context.Context, tape port.Tape) error {
	c.Closed++
	if c.Close != nil {
		return c.Close(ctx, tape)
	}
	return tape.Close()
}

// RequestNext выдаёт следующую кассету и запоминает запрос.
func (c *FuncChanger) RequestNext(
	ctx context.Context,
	req port.NextTapeRequest,
) (port.Tape, domain.TapeLabel, error) {
	c.Requests = append(c.Requests, req)
	if c.Request == nil {
		return nil, domain.TapeLabel{}, fmt.Errorf("funcchanger: запрос кассеты %q без замыкания Request", req.NextTapeName)
	}
	return c.Request(ctx, req)
}
