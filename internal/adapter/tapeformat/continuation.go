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

// Указатель продолжения между кассетами spanning-цепочки: JSON-блок
// в конце кассеты с незавершённой сессией.
//
//	... [tar части][EOF]            ← filemark tar (WriteSession)
//	[блок {"kind":"continuation", ...}]
//	[EOF][EOF]                       ← закрывающая EOD-пара (новый EOD)
//
// Блок лежит между filemark'ом tar и закрывающей парой: только такой
// порядок позволяет последовательному чтению сессий (ReadSession
// подряд) наткнуться на указатель как на «индекс следующей сессии» —
// чтение сегмента индекса останавливается на первом filemark'е.
// Инвариант FORMAT §4 (2K+3 меток) и чтение части голым GNU tar
// (dd до filemark'а после tar) сохраняются. Пару меток после блока
// ставит вызывающий (closeEOD бекапа) — она и есть новый EOD.
//
// Имя следующей кассеты, а не UUID: UUID неизвестен до форматирования.
// Часть на следующей кассете несёт обратную ссылку continues в индексе
// — цепочка читается в обе стороны.

package tapeformat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// continuationKind — значение поля kind блока-указателя продолжения.
const continuationKind = "continuation"

// checkContinuation возвращает *domain.ContinuationError, если данные —
// continuation-блок (последовательное чтение сессий наткнулось на
// указатель продолжения), иначе nil.
func checkContinuation(trimmed []byte) error {
	var cb continuationBlock
	if json.Unmarshal(trimmed, &cb) != nil || cb.Kind != continuationKind {
		return nil
	}
	return &domain.ContinuationError{
		JobRunID:     cb.JobRunID,
		SessionNum:   cb.SessionNum,
		Part:         cb.Part,
		NextTapeName: cb.NextTapeName,
	}
}

// continuationBlock — wire-форма указателя продолжения: JSON, добитый
// нулями до одного блока domain.BlockSize (как ярлык).
type continuationBlock struct {
	Kind         string `json:"kind"`
	JobRunID     string `json:"job_run_id"`
	SessionNum   int32  `json:"session_num"`
	Part         int32  `json:"part"`
	NextTapeName string `json:"next_tape_name"`
}

// WriteContinuation пишет блок-указатель продолжения в текущую позицию
// ленты — сразу после filemark'а tar завершённой части; filemark'и не
// ставит: закрывающую EOD-пару (новый EOD кассеты) пишет вызывающий.
func WriteContinuation(ctx context.Context, tape port.Tape, c port.Continuation) error {
	if c.NextTapeName == "" {
		return fmt.Errorf("tapeformat: указатель продолжения без имени следующей кассеты")
	}
	padded, err := encodeBlocks(continuationBlock{
		Kind:         continuationKind,
		JobRunID:     c.JobRunID,
		SessionNum:   c.SessionNum,
		Part:         c.Part,
		NextTapeName: c.NextTapeName,
	}, 1)
	if err != nil {
		return fmt.Errorf("tapeformat: кодирование указателя продолжения: %w", err)
	}
	if err := tape.WriteBlock(ctx, padded); err != nil {
		return fmt.Errorf("tapeformat: запись указателя продолжения: %w", err)
	}
	return nil
}

// ReadContinuation читает блок-указатель с текущей позиции ленты
// (позиция после filemark'а tar завершённой части). Блока нет (EOD)
// или блок пуст (одни нули) — io.EOF; чужой блок —
// *domain.NotContinuationError. После успеха позиция стоит на
// закрывающей EOD-паре.
func ReadContinuation(ctx context.Context, tape port.Tape) (port.Continuation, error) {
	block, err := tape.ReadBlock(ctx)
	if errors.Is(err, io.EOF) {
		return port.Continuation{}, io.EOF
	}
	if err != nil {
		return port.Continuation{}, fmt.Errorf("tapeformat: чтение указателя продолжения: %w", err)
	}
	trimmed := trimZeros(block)
	if len(trimmed) == 0 {
		return port.Continuation{}, io.EOF
	}
	var cb continuationBlock
	if json.Unmarshal(trimmed, &cb) != nil || cb.Kind != continuationKind {
		return port.Continuation{}, &domain.NotContinuationError{Snippet: snippet(trimmed)}
	}
	return port.Continuation{
		JobRunID:     cb.JobRunID,
		SessionNum:   cb.SessionNum,
		Part:         cb.Part,
		NextTapeName: cb.NextTapeName,
	}, nil
}
