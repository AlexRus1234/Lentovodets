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

// Inspect — инвентарь кассеты без каталога и конфигурации: ярлык и
// индексы всех сессий читаются прямо с ленты (DR-путь самоописываемой
// ленты). tar-потоки не читаются: как у catalog rebuild, инвентарь —
// не верификация (целостность данных проверяет readtest).
// См. docs/SPECIFICATION.md §4.5, docs/FORMAT.md §9, §12.

package inspect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// SessionInfo — сводка одной сессии по её индексу.
type SessionInfo struct {
	Header port.SessionHeader // заголовок индекса: номер, тип, job, часть
	Files  []domain.FileMeta  // записи индекса (включая каталоги и tombstone'ы)
	Bytes  int64              // контент: сумма размеров не-каталогов и не-tombstone'ов
}

// Report — итог осмотра одной кассеты.
type Report struct {
	Label        domain.TapeLabel // ярлык
	Sessions     []SessionInfo    // сессии в порядке следования на ленте
	NextTapeName string           // цепочка продолжается: имя следующей кассеты; "" — кассета самодостаточна
}

// UseCase — осмотр кассеты: только лента и кодек, без каталога и
// конфигурации.
type UseCase struct {
	tape  port.Tape
	codec port.TapeCodec
	log   *slog.Logger
}

// New создаёт use case осмотра кассеты.
func New(tape port.Tape, codec port.TapeCodec, log *slog.Logger) *UseCase {
	return &UseCase{tape: tape, codec: codec, log: log}
}

// Inspect проходит кассету: Rewind → ярлык → подряд индексы всех сессий
// (ReadIndexFiles + пропуск tar-сегмента ForwardFilemarks(1),
// docs/FORMAT.md §9). Повреждённый индекс — ошибка с номером сессии:
// диагностика обязана сообщать о порче, а не пропускать её (как readtest).
// Блок-указатель продолжения — конец осмотра с именем следующей кассеты
// (следование цепочке — за оператором, как у rebuild).
func (uc *UseCase) Inspect(ctx context.Context) (Report, error) {
	rep := Report{}
	if err := uc.tape.Rewind(ctx); err != nil {
		return rep, fmt.Errorf("inspect: перемотка: %w", err)
	}
	label, err := uc.readLabel(ctx)
	if err != nil {
		return rep, err
	}
	rep.Label = label
	// Позиция — на filemark'е ярлыка; переводим на индекс сессии 1
	// (FORMAT §9: чтение сессий подряд начинается после файла ярлыка).
	if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
		return rep, fmt.Errorf("inspect: пропуск ярлыка: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return rep, fmt.Errorf("inspect: %w", err)
		}
		header, files, err := uc.codec.ReadIndexFiles(ctx, uc.tape)
		if isEndOfSessions(err) {
			break // EOD: все сессии кассеты прочитаны
		}
		var cont *domain.ContinuationError
		if errors.As(err, &cont) {
			rep.NextTapeName = cont.NextTapeName
			uc.log.Warn("цепочка продолжается: вставьте следующую кассету",
				slog.String("next_tape", cont.NextTapeName),
				slog.String("job_run_id", cont.JobRunID),
				slog.Int("session_num", int(cont.SessionNum)),
				slog.Int("part", int(cont.Part)))
			break
		}
		if err != nil {
			return rep, fmt.Errorf("inspect: сессия %d: %w", len(rep.Sessions)+1, err)
		}
		if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
			return rep, fmt.Errorf("inspect: пропуск tar сессии %d: %w", header.SessionNum, err)
		}
		rep.Sessions = append(rep.Sessions, SessionInfo{
			Header: header,
			Files:  files,
			Bytes:  contentBytes(files),
		})
	}
	uc.log.Info("inspect завершён",
		slog.String("tape", label.Name),
		slog.Int("sessions", len(rep.Sessions)))
	return rep, nil
}

// readLabel читает и разбирает ярлык с текущей позиции.
func (uc *UseCase) readLabel(ctx context.Context) (domain.TapeLabel, error) {
	block, err := uc.tape.ReadBlock(ctx)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("inspect: чтение ярлыка: %w", labelReadErr(err))
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("inspect: разбор ярлыка: %w", err)
	}
	return label, nil
}

// isEndOfSessions — достигнут EOD (пустой индекс после всех сессий).
func isEndOfSessions(err error) bool {
	if err == nil {
		return false
	}
	var empty *domain.EmptyIndexError
	return errors.As(err, &empty)
}

// labelReadErr превращает io.EOF в BlankTapeError для консистентных
// сообщений.
func labelReadErr(err error) error {
	if errors.Is(err, io.EOF) {
		return &domain.BlankTapeError{}
	}
	return err
}

// contentBytes суммирует размер контента сессии: каталоги и tombstone'ы
// не имеют тела на ленте.
func contentBytes(files []domain.FileMeta) int64 {
	var n int64
	for _, fm := range files {
		if !fm.IsDir && !fm.IsDeleted() {
			n += fm.Size
		}
	}
	return n
}
