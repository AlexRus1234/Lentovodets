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

// Верификация после записи (verify-after-write, сессия 13): обратное
// чтение только что записанных сессий текущего запуска со сверкой
// хешей. LTO ECC ≠ верификация контента — перезапись соседних дорожек
// и ошибки серводанных ловит только повторное чтение; хеши уже в
// индексе, сверка бесплатна по коду и дорога по времени.

package backup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// verifyPart перечитывает только что записанную часть сессии:
// Rewind + MTFSF(2K−1) на индекс сессии K, затем ReadSession в режиме
// проверки (dest = nil — xxhash каждого файла сверяет декодер против
// индекса) и счётная сверка состава с записанным. Вызывается после
// фиксации части в каталоге (SaveFiles) и до плановой смены кассеты —
// после eject (changer.CloseTape) прочитать уже нечем.
//
// Любой сбой — *domain.VerifyError: данные уже на ленте и в каталоге,
// откат бессмыслен (перезапись не лечит носитель), сессия остаётся
// зафиксированной, решение за оператором. Возвращает число сверенных
// файлов и байт данных.
func (uc *UseCase) verifyPart(
	ctx context.Context,
	tape port.Tape,
	sess domain.Session,
	files []domain.FileMeta,
) (int, int64, error) {
	start := uc.clock.Now()
	uc.progUpdate(port.ProgressUpdate{
		Phase:   port.PhaseVerify,
		Message: fmt.Sprintf("верификация сессии %d: обратное чтение со сверкой хешей", sess.Num),
	})
	if err := tape.Rewind(ctx); err != nil {
		return 0, 0, uc.verifyError(sess, "", fmt.Errorf("перемотка: %w", err))
	}
	if err := tape.ForwardFilemarks(ctx, int(2*sess.Num-1)); err != nil {
		return 0, 0, uc.verifyError(sess, "",
			fmt.Errorf("позиционирование MTFSF(%d): %w", 2*sess.Num-1, err))
	}
	read, err := uc.codec.ReadSession(ctx, tape, nil, uc.prog)
	if err != nil {
		return 0, 0, uc.verifyError(sess, "", fmt.Errorf("обратное чтение: %w", err))
	}
	if len(read) != len(files) {
		return 0, 0, uc.verifyError(sess, firstMissingPath(files, read), fmt.Errorf(
			"состав сессии не совпал: записано %d файлов, прочитано %d", len(files), len(read)))
	}
	var bytes int64
	for _, fm := range read {
		if !fm.IsDeleted() && !fm.IsSymlink() && !fm.IsHardlink() {
			bytes += fm.Size
		}
	}
	took := uc.clock.Now().Sub(start)
	uc.log.Info("verify done",
		slog.Int("session_num", int(sess.Num)),
		slog.Int("files", len(read)),
		slog.Int64("bytes", bytes),
		slog.Duration("took", took))
	uc.progUpdate(port.ProgressUpdate{
		Phase: port.PhaseVerify,
		Message: fmt.Sprintf("верификация сессии %d: %d файлов (%d байт) за %s",
			sess.Num, len(read), bytes, took.Round(time.Millisecond)),
	})
	return len(read), bytes, nil
}

// verifyError собирает *domain.VerifyError с деталями причины.
func (uc *UseCase) verifyError(sess domain.Session, file string, cause error) error {
	return fmt.Errorf("backup: %w", &domain.VerifyError{
		SessionNum: sess.Num,
		File:       file,
		Details:    cause.Error(),
	})
}

// firstMissingPath — первый путь files, отсутствующий среди
// прочитанных (заполняет File VerifyError при счётном несовпадении
// состава); пустая строка, если прочитанное — надмножество записанного.
func firstMissingPath(files, read []domain.FileMeta) string {
	set := make(map[string]struct{}, len(read))
	for _, fm := range read {
		set[fm.Path] = struct{}{}
	}
	for _, fm := range files {
		if _, ok := set[fm.Path]; !ok {
			return fm.Path
		}
	}
	return ""
}
