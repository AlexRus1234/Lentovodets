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

// CatalogUseCase — запросы к каталогу и управление лентой для CLI/WEB.
// См. docs/SPECIFICATION.md §4.4–4.5.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/restore"
)

// UseCase — тонкая композиция портов: вся логика в адаптерах.
type UseCase struct {
	cat     port.Catalog
	tape    port.Tape
	codec   port.TapeCodec
	prog    port.ProgressReporter // nil допустим
	log     *slog.Logger
	changer port.TapeChanger // nil — цепочки кассет не следуются
}

// New собирает use case; tape/codec могут быть nil для чисто
// каталожных сценариев (daemon без устройства). changer может быть
// nil: тогда readtest на кассете с указателем продолжения
// завершается Warn'ом «вставьте кассету и перезапустите».
func New(
	cat port.Catalog,
	tape port.Tape,
	codec port.TapeCodec,
	prog port.ProgressReporter,
	log *slog.Logger,
	changer port.TapeChanger,
) *UseCase {
	return &UseCase{
		cat: cat, tape: tape, codec: codec,
		prog: prog, log: log, changer: changer,
	}
}

// ListTapes — все кассеты каталога.
func (uc *UseCase) ListTapes(ctx context.Context) ([]port.TapeRecord, error) {
	tapes, err := uc.cat.ListTapes(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog: список кассет: %w", err)
	}
	return tapes, nil
}

// ListSessions — сессии; tapeUUID == "" — по всем кассетам.
func (uc *UseCase) ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error) {
	sessions, err := uc.cat.ListSessions(ctx, tapeUUID)
	if err != nil {
		return nil, fmt.Errorf("catalog: список сессий: %w", err)
	}
	return sessions, nil
}

// GetFiles — все файлы сессии.
func (uc *UseCase) GetFiles(ctx context.Context, sessionID int64) ([]domain.FileMeta, error) {
	files, err := uc.cat.GetFilesBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("catalog: файлы сессии %d: %w", sessionID, err)
	}
	return files, nil
}

// Search — глобальный поиск файлов по подстроке пути.
func (uc *UseCase) Search(ctx context.Context, pattern string) ([]port.FileCopy, error) {
	copies, err := uc.cat.SearchFiles(ctx, pattern)
	if err != nil {
		return nil, fmt.Errorf("catalog: поиск %q: %w", pattern, err)
	}
	return copies, nil
}

// Copies — все копии точного пути, от новых к старым.
func (uc *UseCase) Copies(ctx context.Context, path string) ([]port.FileCopy, error) {
	copies, err := uc.cat.GetAllFileCopies(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("catalog: копии %q: %w", path, err)
	}
	return copies, nil
}

// DeleteSession удаляет сессию из каталога (данные на ленте остаются).
func (uc *UseCase) DeleteSession(ctx context.Context, sessionID int64) error {
	if err := uc.cat.DeleteSession(ctx, sessionID); err != nil {
		return fmt.Errorf("catalog: удаление сессии %d: %w", sessionID, err)
	}
	return nil
}

// Prune удаляет сессии старше before (Unix-секунды) и возвращает число
// удалённых.
func (uc *UseCase) Prune(ctx context.Context, before int64) (int64, error) {
	n, err := uc.cat.PruneSessions(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("catalog: очистка до %d: %w", before, err)
	}
	return n, nil
}

// TapeInfo читает ярлык установленной ленты.
func (uc *UseCase) TapeInfo(ctx context.Context) (domain.TapeInfo, error) {
	if err := uc.tape.Rewind(ctx); err != nil {
		return domain.TapeInfo{}, fmt.Errorf("catalog: перемотка: %w", err)
	}
	label, err := uc.readLabel(ctx, "catalog")
	if err != nil {
		return domain.TapeInfo{}, err
	}
	return domain.TapeInfo{Label: label, Filemark: -1}, nil
}

// Eject извлекает ленту (MTOFFL).
func (uc *UseCase) Eject(ctx context.Context) error {
	if err := uc.tape.Eject(ctx); err != nil {
		return fmt.Errorf("catalog: извлечение ленты: %w", err)
	}
	return nil
}

// TapeReport — итог проверки одной кассеты цепочки readtest.
type TapeReport struct {
	Name     string // имя кассеты из ярлыка
	Sessions int    // сессий проверено
	Files    int    // файлов в проверенных сессиях (без каталогов/tombstone'ов)
	Bytes    int64  // суммарный размер проверенных файлов
}

// ReadTest прогоняет ленту в режиме проверки: чтение всех сессий
// с сверкой хешей без записи на ФС. Повреждённая сессия — ошибка.
// Кассета с указателем продолжения — часть spanning-цепочки: при
// заданном changer проверка следует цепочке (смена кассеты со
// сверкой, ChainFollower), без changer'а — Warn «вставьте кассету»
// и конец. Возвращает отчёт по каждой кассете цепочки.
func (uc *UseCase) ReadTest(ctx context.Context) ([]TapeReport, error) {
	if err := uc.tape.Rewind(ctx); err != nil {
		return nil, fmt.Errorf("readtest: перемотка: %w", err)
	}
	label, err := uc.readLabel(ctx, "readtest")
	if err != nil {
		return nil, err
	}
	// Позиция — на filemark'е ярлыка; переводим на индекс сессии 1
	// (FORMAT §9: чтение сессий подряд начинается после файла ярлыка).
	if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
		return nil, fmt.Errorf("readtest: пропуск ярлыка: %w", err)
	}
	cur := restore.TapeState{Tape: uc.tape, Label: label}
	reports := []TapeReport{{Name: label.Name}}
	rep := &reports[0]
	for {
		if err := ctx.Err(); err != nil {
			return reports, fmt.Errorf("readtest: %w", err)
		}
		files, err := uc.codec.ReadSession(ctx, cur.Tape, nil, uc.prog)
		if isEndOfSessions(err) {
			break
		}
		var cont *domain.ContinuationError
		if errors.As(err, &cont) {
			if uc.changer == nil {
				uc.log.Warn("кассета имеет продолжение: вставьте следующую и перезапустите readtest",
					slog.String("next_tape", cont.NextTapeName),
					slog.String("job_run_id", cont.JobRunID),
					slog.Int("session_num", int(cont.SessionNum)),
					slog.Int("part", int(cont.Part)))
				break
			}
			next, ferr := uc.followChain(ctx, cur, cont)
			if ferr != nil {
				return reports, fmt.Errorf("readtest: %w", ferr)
			}
			cur = next
			reports = append(reports, TapeReport{Name: cur.Label.Name})
			rep = &reports[len(reports)-1]
			continue
		}
		if err != nil {
			return reports, fmt.Errorf("readtest: сессия %d: %w", rep.Sessions+1, err)
		}
		rep.Sessions++
		rep.Files += countCheckedFiles(files)
		rep.Bytes += checkedBytes(files)
	}
	if uc.prog != nil {
		uc.prog.Done()
	}
	return reports, nil
}

// followChain делегирует смену кассеты ChainFollower (общий с
// RestoreFull) с зависимостями use case'а.
func (uc *UseCase) followChain(ctx context.Context, cur restore.TapeState, cont *domain.ContinuationError) (restore.TapeState, error) {
	f := &restore.ChainFollower{
		Changer: uc.changer, Codec: uc.codec, Cat: uc.cat,
		Prog: uc.prog, Log: uc.log,
	}
	return f.Follow(ctx, cur, cont)
}

// countCheckedFiles считает файлы (без каталогов и tombstone'ов).
func countCheckedFiles(files []domain.FileMeta) int {
	n := 0
	for _, fm := range files {
		if !fm.IsDir && !fm.IsDeleted() {
			n++
		}
	}
	return n
}

// checkedBytes суммирует размеры файлов (без каталогов и tombstone'ов).
func checkedBytes(files []domain.FileMeta) int64 {
	var n int64
	for _, fm := range files {
		if !fm.IsDir && !fm.IsDeleted() {
			n += fm.Size
		}
	}
	return n
}

// readLabel читает ярлык с BOT; op — префикс ошибок вызывающей операции.
func (uc *UseCase) readLabel(ctx context.Context, op string) (domain.TapeLabel, error) {
	block, err := uc.tape.ReadBlock(ctx)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("%s: чтение ярлыка: %w", op, labelReadErr(err))
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("%s: разбор ярлыка: %w", op, err)
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
