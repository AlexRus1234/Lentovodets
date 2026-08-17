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

// Смена кассет для local-бекапа: промпт оператору, форматирование
// и регистрация новой кассеты (план spanning, сессия 5). Daemon-режим
// получит свою реализацию (pause/resume) позже.

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/format"
)

// stdinChanger — port.TapeChanger поверх терминала: извлекает
// закрытую кассету, спрашивает имя следующей (Enter принимает
// предложенное), открывает устройство, форматирует и регистрирует
// кассету в каталоге.
type stdinChanger struct {
	deps     Deps
	cfg      ConfigFile
	cat      port.Catalog
	codec    port.TapeCodec
	rand     port.Rand
	clock    port.Clock
	log      *slog.Logger
	job      string // задание бекапа (для имени-фолбэка <job>-002)
	nextName string // --next-tape: неинтерактивный режим; "" — спросить
}

// CloseTape извлекает и закрывает текущую ленту.
func (c *stdinChanger) CloseTape(ctx context.Context, tape port.Tape) error {
	return ejectClose(ctx, tape)
}

// RequestNext спрашивает имя кассеты (если нужно), открывает
// устройство, форматирует кассету этим именем и регистрирует её
// в каталоге.
func (c *stdinChanger) RequestNext(
	ctx context.Context,
	req port.NextTapeRequest,
) (port.Tape, domain.TapeLabel, error) {
	suggested := req.NextTapeName
	if suggested == "" {
		suggested = domain.NextTapeName(req.JobName, req.FinishedTape)
	}
	name, err := c.askName(req, suggested)
	if err != nil {
		return nil, domain.TapeLabel{}, err
	}
	tape, err := c.deps.OpenTape(c.cfg.Device())
	if err != nil {
		return nil, domain.TapeLabel{}, err
	}
	label, err := format.New(tape, c.codec, c.cat, c.rand, c.clock, c.log).Format(ctx, name, false)
	if err != nil {
		var already *domain.AlreadyFormattedError
		if errors.As(err, &already) {
			err = fmt.Errorf(
				"вставленная кассета не чиста (%s); вставьте чистую и повторите запуск: %w",
				already.Name, err)
		}
		if cerr := tape.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("закрытие ленты: %w", cerr))
		}
		return nil, domain.TapeLabel{}, fmt.Errorf("форматирование кассеты %s: %w", name, err)
	}
	return tape, label, nil
}

// SuggestNextName предлагает имя следующей кассеты: инкремент
// числового суффикса текущей, иначе <job>-002.
func (c *stdinChanger) SuggestNextName(_ context.Context, current string) (string, error) {
	return domain.NextTapeName(c.job, current), nil
}

// askName определяет имя новой кассеты: флаг --next-tape для
// неинтерактивного режима, иначе промпт (Enter принимает предложение).
func (c *stdinChanger) askName(req port.NextTapeRequest, suggested string) (string, error) {
	if c.nextName != "" {
		return c.nextName, nil
	}
	if !c.deps.IsInteractive() {
		return "", fmt.Errorf(
			"нужна следующая кассета (%s), но ввод не интерактивен; задайте имя флагом --next-tape",
			req.Reason)
	}
	fmt.Fprintf(c.deps.Stderr, "Кассета %s закрыта. Вставьте чистую кассету, имя [%s]: ",
		req.FinishedTape, suggested)
	line, err := readLine(bufio.NewScanner(c.deps.Stdin))
	if err != nil {
		return "", fmt.Errorf("чтение имени кассеты: %w", err)
	}
	if line == "" {
		return suggested, nil
	}
	return line, nil
}

// restoreChanger — port.TapeChanger для чтения цепочки кассет
// (restore full и tape readtest): оператора просят вставить
// конкретную кассету — её имя известно из указателя продолжения,
// поэтому ввод имени не нужен. Кассета НЕ форматируется: данные
// на ней уже записаны; ярлык читается и возвращается для сверки
// цепочки (usecase проверит имя и обратную ссылку continues).
type restoreChanger struct {
	deps  Deps
	cfg   ConfigFile
	codec port.TapeCodec
}

// CloseTape извлекает и закрывает прочитанную ленту.
func (c *restoreChanger) CloseTape(ctx context.Context, tape port.Tape) error {
	return ejectClose(ctx, tape)
}

// RequestNext ждёт, пока оператор вставит кассету цепочки (Enter),
// открывает устройство и читает ярлык вставленной кассеты.
func (c *restoreChanger) RequestNext(
	ctx context.Context,
	req port.NextTapeRequest,
) (port.Tape, domain.TapeLabel, error) {
	if !c.deps.IsInteractive() {
		return nil, domain.TapeLabel{}, fmt.Errorf(
			"нужна кассета %s (часть %d), но ввод не интерактивен; вставьте её и перезапустите",
			req.NextTapeName, req.Part)
	}
	fmt.Fprintf(c.deps.Stderr, "Кассета %s прочитана. Вставьте кассету %s (часть %d) и нажмите Enter: ",
		req.FinishedTape, req.NextTapeName, req.Part)
	if _, err := readLine(bufio.NewScanner(c.deps.Stdin)); err != nil {
		return nil, domain.TapeLabel{}, fmt.Errorf("ожидание кассеты %s: %w", req.NextTapeName, err)
	}
	tape, err := c.deps.OpenTape(c.cfg.Device())
	if err != nil {
		return nil, domain.TapeLabel{}, err
	}
	label, err := readTapeLabel(ctx, tape, c.codec)
	if err != nil {
		if cerr := tape.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("закрытие ленты: %w", cerr))
		}
		return nil, domain.TapeLabel{}, err
	}
	return tape, label, nil
}

// SuggestNextName неприменим: имя следующей кассеты известно из
// указателя продолжения на ленте.
func (c *restoreChanger) SuggestNextName(context.Context, string) (string, error) {
	return "", errors.New("restore: имя кассеты цепочки известно из указателя продолжения")
}

// ejectClose извлекает ленту и закрывает устройство.
func ejectClose(ctx context.Context, tape port.Tape) error {
	if err := tape.Eject(ctx); err != nil {
		return fmt.Errorf("извлечение кассеты: %w", err)
	}
	if err := tape.Close(); err != nil {
		return fmt.Errorf("закрытие ленты: %w", err)
	}
	return nil
}

// readTapeLabel читает и разбирает ярлык установленной кассеты.
func readTapeLabel(ctx context.Context, tape port.Tape, codec port.TapeCodec) (domain.TapeLabel, error) {
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
