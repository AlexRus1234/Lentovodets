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

// Rebuild — реконструкция каталога из содержимого вставленной кассеты:
// ярлык → кассета, JSON-индексы сессий → сессии и файлы. Лента —
// источник истины, каталог — производное. tar-поток не читается:
// rebuild ≠ верификация (целостность проверяет readtest).
// См. docs/SPECIFICATION.md §4.4, docs/FORMAT.md §9.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// RebuildReport — итог реконструкции каталога с одной кассеты.
type RebuildReport struct {
	TapeName        string // имя кассеты из ярлыка
	Sessions        int    // добавлено сессий
	Files           int    // файлов в добавленных сессиях
	SkippedSessions int    // уже были в каталоге — пропущены (идемпотентность)
	NextTapeName    string // цепочка продолжается: вставить эту кассету и повторить; "" — нет
}

// Rebuild пересобирает каталог из индексов вставленной кассеты:
// Rewind → ярлык → RegisterTape → подряд индексы всех сессий
// (ReadIndexFiles + пропуск tar-сегмента ForwardFilemarks(1),
// docs/FORMAT.md §9) → CreateSession + SaveFiles. Повторный запуск
// безопасен: существующая сессия (UNIQUE tape_uuid+session_num)
// пропускается, не дублируется и не перезаписывается. Кассета с
// указателем продолжения — Warn и конец прогона: данные прочитанной
// части уже в каталоге, продолжение — вставить следующую кассету и
// повторить rebuild (DR-семантика, без авто-смены в v1).
func (uc *UseCase) Rebuild(ctx context.Context) (RebuildReport, error) {
	rep := RebuildReport{}
	if err := uc.tape.Rewind(ctx); err != nil {
		return rep, fmt.Errorf("rebuild: перемотка: %w", err)
	}
	label, err := uc.readLabel(ctx, "rebuild")
	if err != nil {
		return rep, err
	}
	// DecodeLabel валидирует formatted_at, но порт допускает любые
	// реализации кодека — сверяем ещё раз на месте.
	formattedAt, err := label.ParseFormattedAt()
	if err != nil {
		return rep, fmt.Errorf("rebuild: %w", err)
	}
	if err := uc.cat.RegisterTape(ctx, label.UUID, label.Name, formattedAt.Unix()); err != nil {
		return rep, fmt.Errorf("rebuild: регистрация кассеты: %w", err)
	}
	existing, err := uc.existingSessionNums(ctx, label.UUID)
	if err != nil {
		return rep, err
	}
	// Позиция — на filemark'е ярлыка; переводим на индекс сессии 1
	// (FORMAT §9: чтение сессий подряд начинается после файла ярлыка).
	if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
		return rep, fmt.Errorf("rebuild: пропуск ярлыка: %w", err)
	}
	rep.TapeName = label.Name
	for {
		if err := ctx.Err(); err != nil {
			return rep, fmt.Errorf("rebuild: %w", err)
		}
		header, files, err := uc.codec.ReadIndexFiles(ctx, uc.tape)
		if isEndOfSessions(err) {
			break // EOD: все сессии кассеты прочитаны
		}
		var cont *domain.ContinuationError
		if errors.As(err, &cont) {
			rep.NextTapeName = cont.NextTapeName
			uc.log.Warn("цепочка продолжается: вставьте кассету и повторите rebuild",
				slog.String("next_tape", cont.NextTapeName),
				slog.String("job_run_id", cont.JobRunID),
				slog.Int("session_num", int(cont.SessionNum)),
				slog.Int("part", int(cont.Part)))
			break
		}
		if err != nil {
			return rep, fmt.Errorf("rebuild: сессия %d: %w", rep.Sessions+rep.SkippedSessions+1, err)
		}
		if err := uc.rebuildSession(ctx, label, existing, header, files, &rep); err != nil {
			return rep, err
		}
	}
	uc.log.Info("rebuild завершён",
		slog.String("tape", label.Name),
		slog.Int("sessions", rep.Sessions),
		slog.Int("files", rep.Files),
		slog.Int("skipped", rep.SkippedSessions))
	return rep, nil
}

// rebuildSession переносит одну прочитанную сессию в каталог (или
// пропускает существующую) и продвигает ленту за её tar-сегмент —
// на индекс следующей сессии.
func (uc *UseCase) rebuildSession(
	ctx context.Context,
	label domain.TapeLabel,
	existing map[int32]bool,
	header port.SessionHeader,
	files []domain.FileMeta,
	rep *RebuildReport,
) error {
	if existing[header.SessionNum] {
		rep.SkippedSessions++
	} else {
		id, err := uc.cat.CreateSession(ctx, domain.Session{
			TapeUUID:  label.UUID,
			Num:       header.SessionNum,
			Type:      header.Type,
			Timestamp: header.Timestamp,
			JobRunID:  header.JobRunID,
			Part:      header.Part,
		})
		if err != nil {
			return fmt.Errorf("rebuild: сессия %d: %w", header.SessionNum, err)
		}
		if err := uc.cat.SaveFiles(ctx, id, files); err != nil {
			return fmt.Errorf("rebuild: файлы сессии %d: %w", header.SessionNum, err)
		}
		rep.Sessions++
		rep.Files += len(files)
	}
	if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
		return fmt.Errorf("rebuild: пропуск tar сессии %d: %w", header.SessionNum, err)
	}
	return nil
}

// existingSessionNums — множество номеров сессий кассеты, уже
// записанных в каталог (для идемпотентного пропуска).
func (uc *UseCase) existingSessionNums(ctx context.Context, tapeUUID string) (map[int32]bool, error) {
	sessions, err := uc.cat.ListSessions(ctx, tapeUUID)
	if err != nil {
		return nil, fmt.Errorf("rebuild: сессии кассеты %s: %w", tapeUUID, err)
	}
	existing := make(map[int32]bool, len(sessions))
	for _, sess := range sessions {
		existing[sess.Num] = true
	}
	return existing, nil
}
