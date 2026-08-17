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

// RestoreUseCase — восстановление с ленты: Full / Selective / Smart.
// См. docs/SPECIFICATION.md §4.3, docs/ARCHITECTURE.md §4.2.

package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// UseCase восстанавливает файлы с ленты на файловую систему.
type UseCase struct {
	tape    port.Tape
	codec   port.TapeCodec
	cat     port.Catalog
	fs      port.Filesystem
	prog    port.ProgressReporter // nil допустим
	log     *slog.Logger
	changer port.TapeChanger // nil — цепочки кассет не следуются
}

// New собирает use case (wire в iface/cli). changer может быть nil:
// тогда указатель продолжения на границе кассет завершает Full
// с Warn «вставьте кассету и перезапустите» (полезно и без
// интерактива), данные прочитанной части остаются на месте.
func New(
	tape port.Tape,
	codec port.TapeCodec,
	cat port.Catalog,
	fs port.Filesystem,
	prog port.ProgressReporter,
	log *slog.Logger,
	changer port.TapeChanger,
) *UseCase {
	return &UseCase{
		tape: tape, codec: codec, cat: cat, fs: fs,
		prog: prog, log: log, changer: changer,
	}
}

// Stats — итог восстановления.
type Stats struct {
	Sessions int // сессий прочитано (Full)
	Files    int // файлов восстановлено (без каталогов)
	Dirs     int // каталогов создано
	Skipped  int // записей пропущено (не запрошенные пути)
}

// Full восстанавливает все сессии ленты подряд, от BOT до EOD.
// Повреждённая сессия не прерывает восстановление: она логируется
// (Warn) и пропускается. Кассета с указателем продолжения — часть
// spanning-цепочки: при заданном changer восстановление следует
// цепочке (смена кассеты со сверкой ярлыка и обратной ссылки,
// ChainFollower), без changer'а — Warn «вставьте кассету» и конец
// (файлы прочитанной части уже восстановлены). Возвращает суммарную
// статистику по всем кассетам цепочки.
func (uc *UseCase) Full(ctx context.Context) (Stats, error) {
	if err := uc.tape.Rewind(ctx); err != nil {
		return Stats{}, fmt.Errorf("restore full: перемотка в начало: %w", err)
	}
	label, err := uc.readLabel(ctx)
	if err != nil {
		return Stats{}, err
	}
	// Позиция — на filemark'е ярлыка; переводим на индекс сессии 1
	// (FORMAT §9: чтение сессий подряд начинается после файла ярлыка).
	if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
		return Stats{}, fmt.Errorf("restore full: пропуск ярлыка: %w", err)
	}
	cur := TapeState{Tape: uc.tape, Label: label}
	var total Stats
	for {
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("restore full: %w", err)
		}
		files, err := uc.codec.ReadSession(ctx, cur.Tape, uc.fs, uc.prog)
		var empty *domain.EmptyIndexError
		if errors.As(err, &empty) {
			break // EOD: сессии закончились
		}
		var cont *domain.ContinuationError
		if errors.As(err, &cont) {
			if uc.changer == nil {
				uc.log.Warn("кассета имеет продолжение: вставьте следующую и перезапустите восстановление",
					slog.String("next_tape", cont.NextTapeName),
					slog.String("job_run_id", cont.JobRunID),
					slog.Int("session_num", int(cont.SessionNum)),
					slog.Int("part", int(cont.Part)))
				break
			}
			next, ferr := uc.followChain(ctx, cur, cont)
			if ferr != nil {
				return total, fmt.Errorf("restore full: %w", ferr)
			}
			cur = next
			continue
		}
		if err != nil {
			if isSessionDamage(err) {
				uc.log.Warn("повреждённая сессия пропущена",
					slog.String("error", err.Error()))
				total.Sessions++
				uc.skipToNextSession(ctx)
				continue
			}
			return total, fmt.Errorf("restore full: чтение сессии %d: %w", total.Sessions+1, err)
		}
		total.Sessions++
		total.Files += countFiles(files, nil)
		total.Dirs += countDirs(files)
		uc.applyTombstones(files, nil)
	}
	if uc.prog != nil {
		uc.prog.Done()
	}
	return total, nil
}

// followChain делегирует смену кассеты ChainFollower с зависимостями
// use case'а.
func (uc *UseCase) followChain(ctx context.Context, cur TapeState, cont *domain.ContinuationError) (TapeState, error) {
	f := &ChainFollower{
		Changer: uc.changer, Codec: uc.codec, Cat: uc.cat,
		Prog: uc.prog, Log: uc.log,
	}
	return f.Follow(ctx, cur, cont)
}

// Selective восстанавливает выбранные пути из сессии sessionID.
// dest nil/"" — восстановление по исходным путям из индекса.
// Файл части spanning-цепочки лежит целиком на кассете своей части:
// позиционирование идёт в пределах установленной ленты, и если
// сессия принадлежит другой кассете — ошибка с именем кассеты,
// которую нужно вставить (следование цепочке — только у Full).
func (uc *UseCase) Selective(ctx context.Context, sessionID int64, paths []string) (Stats, error) {
	sess, err := uc.cat.ListSessions(ctx, "")
	if err != nil {
		return Stats{}, fmt.Errorf("restore selective: список сессий: %w", err)
	}
	var target *domain.Session
	for i := range sess {
		if sess[i].ID == sessionID {
			target = &sess[i]
			break
		}
	}
	if target == nil {
		return Stats{}, &domain.SessionNotFoundError{SessionID: sessionID}
	}
	if err := uc.checkTapeFor(ctx, target); err != nil {
		return Stats{}, err
	}
	if err := uc.positionToSession(ctx, target.Num); err != nil {
		return Stats{}, err
	}
	files, err := uc.codec.ReadSession(ctx, uc.tape, uc.fs, uc.prog)
	if err != nil {
		return Stats{}, fmt.Errorf("restore selective: чтение сессии %d: %w", sessionID, err)
	}
	want := setOf(paths)
	st := Stats{Files: countFiles(files, want), Dirs: countDirs(files)}
	for _, fm := range files {
		if len(want) > 0 && !want[fm.Path] && !underAny(fm.Path, want) {
			st.Skipped++
		}
	}
	uc.applyTombstones(files, want)
	if uc.prog != nil {
		uc.prog.Done()
	}
	return st, nil
}

// Smart восстанавливает пути по самым свежим читаемым копиям с текущей
// ленты. Порядок копий — от самых новых (порт Catalog упорядочивает по
// убыванию времени). Ошибки чтения конкретной копии (повреждение,
// несовпавший хеш) — переход к следующей копии; ни одна не читается —
// *domain.NoHealthyCopyError.
func (uc *UseCase) Smart(ctx context.Context, paths []string) (Stats, error) {
	if len(paths) == 0 {
		return Stats{}, errors.New("restore smart: список путей пуст")
	}
	label, err := uc.readLabelAfterRewind(ctx)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{}
	remaining := make([]string, 0, len(paths))
	for _, p := range paths {
		remaining = append(remaining, domain.NormalizePath(p))
	}
	// Копии, сгруппированные по сессиям, от новых к старым.
	ordered, err := uc.copiesBySession(ctx, remaining, label.UUID)
	if err != nil {
		return Stats{}, err
	}
	for _, group := range ordered {
		if len(remaining) == 0 {
			break
		}
		if err := uc.positionToSession(ctx, group.num); err != nil {
			uc.log.Warn("smart: позиционирование не удалось",
				slog.Int64("session_id", group.id),
				slog.Int("session_num", int(group.num)),
				slog.String("error", err.Error()))
			continue
		}
		files, readErr := uc.codec.ReadSession(ctx, uc.tape, uc.fs, uc.prog)
		if readErr != nil {
			uc.log.Warn("smart: копия не читается",
				slog.Int64("session_id", group.id),
				slog.Int("session_num", int(group.num)),
				slog.String("error", readErr.Error()))
			continue
		}
		st.Files += countFiles(files, setOf(remaining))
		st.Dirs += countDirs(files)
		uc.applyTombstones(files, setOf(remaining))
		remaining = dropRestored(remaining, files)
	}
	if len(remaining) > 0 {
		err := &domain.NoHealthyCopyError{Path: strings.Join(remaining, ", ")}
		if uc.prog != nil {
			uc.prog.Fail(err)
		}
		return st, err
	}
	if uc.prog != nil {
		uc.prog.Done()
	}
	return st, nil
}

// sessionCopies — сессии, содержащие хотя бы один из искомых путей.
type sessionCopies struct {
	id       int64
	num      int32
	tapeUUID string
}

// copiesBySession возвращает сессии с копиями путей, упорядоченные
// от новых к старым (порядок GetAllFileCopies).
func (uc *UseCase) copiesBySession(ctx context.Context, paths []string, tapeUUID string) ([]sessionCopies, error) {
	var ordered []sessionCopies
	seen := make(map[int64]bool)
	for _, p := range paths {
		copies, err := uc.cat.GetAllFileCopies(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("restore smart: копии %q: %w", p, err)
		}
		for _, cp := range copies {
			if cp.TapeUUID != tapeUUID || seen[cp.SessionID] {
				continue
			}
			seen[cp.SessionID] = true
			ordered = append(ordered, sessionCopies{
				id: cp.SessionID, num: cp.SessionNum, tapeUUID: cp.TapeUUID,
			})
		}
	}
	return ordered, nil
}

// checkTapeFor сверяет кассету сессии с установленной: selective
// позиционируется в пределах текущей ленты, файл части цепочки лежит
// на кассете своей части — оператору говорится, какую кассету
// вставить.
func (uc *UseCase) checkTapeFor(ctx context.Context, target *domain.Session) error {
	label, err := uc.readLabelAfterRewind(ctx)
	if err != nil {
		return fmt.Errorf("restore selective: %w", err)
	}
	if label.UUID == target.TapeUUID {
		return nil
	}
	name := target.TapeUUID
	if rec, rerr := uc.cat.GetTapeByUUID(ctx, target.TapeUUID); rerr == nil {
		name = rec.Name
	}
	return fmt.Errorf(
		"restore selective: сессия %d на другой кассете — вставьте %s: %w",
		target.ID, name, &domain.LabelMismatchError{Expected: target.TapeUUID, Actual: label.UUID})
}

// positionToSession перематывает ленту на индекс сессии num:
// MTREW, затем MTFSF(2K-1) (docs/FORMAT.md §9).
func (uc *UseCase) positionToSession(ctx context.Context, num int32) error {
	if num < 1 {
		return fmt.Errorf("restore: недопустимый номер сессии %d", num)
	}
	if err := uc.tape.Rewind(ctx); err != nil {
		return fmt.Errorf("restore: перемотка в начало: %w", err)
	}
	if err := uc.tape.ForwardFilemarks(ctx, int(2*num-1)); err != nil {
		return fmt.Errorf("restore: позиционирование на сессию %d: %w", num, err)
	}
	return nil
}

// readLabel читает ярлык с BOT и возвращает его, не проверяя каталог.
func (uc *UseCase) readLabel(ctx context.Context) (domain.TapeLabel, error) {
	block, err := uc.tape.ReadBlock(ctx)
	if errors.Is(err, io.EOF) {
		return domain.TapeLabel{}, fmt.Errorf("restore: %w", &domain.BlankTapeError{})
	}
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("restore: чтение ярлыка: %w", err)
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("restore: разбор ярлыка: %w", err)
	}
	return label, nil
}

// readLabelAfterRewind перематывает в BOT и читает ярлык.
func (uc *UseCase) readLabelAfterRewind(ctx context.Context) (domain.TapeLabel, error) {
	if err := uc.tape.Rewind(ctx); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("restore: перемотка: %w", err)
	}
	return uc.readLabel(ctx)
}

// skipToNextSession продвигает ленту за повреждённую сессию: до двух
// filemark'ов вперёд (индекс и tar); нехватка меток — конец ленты,
// это не ошибка.
func (uc *UseCase) skipToNextSession(ctx context.Context) {
	for i := 0; i < 2; i++ {
		if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
			uc.log.Warn("конец ленты при пропуске повреждённой сессии",
				slog.String("error", err.Error()))
			return
		}
	}
}

// applyTombstones удаляет из целевой ФС файлы, помеченные tombstone'ами
// (state D) — реконструкция mirror (FORMAT §8). want не пуст — только
// запрошенные пути. Отсутствующий файл — не ошибка (его уже нет).
func (uc *UseCase) applyTombstones(files []domain.FileMeta, want map[string]bool) {
	if files == nil {
		return
	}
	for _, fm := range files {
		if !fm.IsDeleted() {
			continue
		}
		if len(want) > 0 && !want[fm.Path] {
			continue
		}
		if err := uc.fs.Remove(fm.Path); err != nil {
			uc.log.Warn("tombstone: не удаётся удалить",
				slog.String("path", fm.Path),
				slog.String("error", err.Error()))
		}
	}
}

// isSessionDamage сообщает, что ошибка чтения — повреждение данных
// сессии (лечится пропуском), а не сбой транспорта (лечится остановкой).
func isSessionDamage(err error) bool {
	var empty *domain.EmptyIndexError
	if errors.As(err, &empty) {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"повреждён", "повреждена", "разбор индекса", "неожидаемый тип", "отсутствует в индексе"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// countFiles считает файлы (не каталоги, не tombstone'ы); want не пуст —
// только запрошенные пути и их поддеревья.
func countFiles(files []domain.FileMeta, want map[string]bool) int {
	n := 0
	for _, fm := range files {
		if fm.IsDir || fm.IsDeleted() {
			continue
		}
		if len(want) > 0 && !want[fm.Path] && !underAny(fm.Path, want) {
			continue
		}
		n++
	}
	return n
}

// countDirs считает каталоги.
func countDirs(files []domain.FileMeta) int {
	n := 0
	for _, fm := range files {
		if fm.IsDir {
			n++
		}
	}
	return n
}

// underAny сообщает, что путь лежит под одним из want (восстановление
// каталога целиком по запросу корня).
func underAny(p string, want map[string]bool) bool {
	for root := range want {
		if strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// setOf собирает слайс в множество нормализованных путей.
func setOf(paths []string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		m[domain.NormalizePath(p)] = true
	}
	return m
}

// dropRestored убирает из remaining пути, восстановленные в files.
func dropRestored(remaining []string, files []domain.FileMeta) []string {
	restored := make(map[string]bool, len(files))
	for _, fm := range files {
		restored[fm.Path] = true
	}
	out := remaining[:0]
	for _, p := range remaining {
		if !restored[p] {
			out = append(out, p)
		}
	}
	return out
}
