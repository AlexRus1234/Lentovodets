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

// Порт смены кассет для spanning-бекапа. См. логи/тома.md §4 и план
// сессии 5: use case не знает, откуда берётся кассета (stdin-промпт
// CLI — здесь; pause/resume демона — отдельная реализация).

package port

import (
	"context"

	"lentovodec/internal/domain"
)

// Причины запроса следующей кассеты (NextTapeRequest.Reason).
const (
	// ReasonSpan — плановая смена: записанная часть не последняя.
	ReasonSpan = "span"

	// ReasonEnospc — аварийная: часть не влезла в остаток кассеты,
	// оценка ёмкости промахнулась.
	ReasonEnospc = "enospc"

	// ReasonRestore — чтение цепочки кассет: восстановление или
	// readtest дошли до указателя продолжения, нужна следующая
	// кассета цепочки (сессия 6 плана spanning).
	ReasonRestore = "restore"
)

// NextTapeRequest — запрос следующей кассеты цепочки.
type NextTapeRequest struct {
	JobName      string // задание бекапа
	FinishedTape string // имя кассеты, которая закрыта
	NextTapeName string // предложенное имя следующей (SuggestNextName)
	Part         int32  // часть, которая будет писаться на новую кассету
	Reason       string // ReasonSpan | ReasonEnospc
}

// TapeChanger управляет сменой кассеты по запросу use case.
type TapeChanger interface {
	// CloseTape закрывает/извлекает текущую ленту.
	CloseTape(ctx context.Context, tape Tape) error

	// RequestNext получает следующую кассету: взаимодействие
	// с оператором, форматирование, регистрация в каталоге.
	// Возвращает открытую ленту и её ярлык.
	RequestNext(ctx context.Context, req NextTapeRequest) (Tape, domain.TapeLabel, error)

	// SuggestNextName предлагает имя следующей кассеты: оно решено
	// до записи блока-указателя продолжения на текущей кассете.
	SuggestNextName(ctx context.Context, current string) (string, error)
}
