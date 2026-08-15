// FormatTapeUseCase — форматирование кассеты: ярлык + двойной EOF +
// регистрация в каталоге. См. docs/SPECIFICATION.md §4.1,
// docs/ARCHITECTURE.md §4.3.

package format

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// UseCase форматирует ленту по алгоритму ARCHITECTURE §4.3.
type UseCase struct {
	tape  port.Tape
	codec port.TapeCodec
	cat   port.Catalog
	rand  port.Rand
	clock port.Clock
	log   *slog.Logger
}

// New собирает use case. Лента, кодек, каталог, источники случайности
// и времени передаются снаружи (wire в iface/cli).
func New(
	tape port.Tape,
	codec port.TapeCodec,
	cat port.Catalog,
	rand port.Rand,
	clock port.Clock,
	log *slog.Logger,
) *UseCase {
	return &UseCase{tape: tape, codec: codec, cat: cat, rand: rand, clock: clock, log: log}
}

// Format форматирует ленту именем name и возвращает записанный ярлык.
//
// Правила повторного форматирования:
//   - читаемый ярлык нашего семейства без force →
//     *domain.AlreadyFormattedError;
//   - пустая лента и чужой формат — форматировать можно;
//   - ярлык более новой версии формата — ошибка в любом режиме
//     (нужен более новый бинарь; force её не перекрывает).
func (uc *UseCase) Format(ctx context.Context, name string, force bool) (domain.TapeLabel, error) {
	if name == "" {
		return domain.TapeLabel{}, errors.New("format: имя кассеты не может быть пустым")
	}
	if err := uc.tape.Rewind(ctx); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("format: перемотка в начало: %w", err)
	}

	old, formatted, err := uc.readLabel(ctx)
	if err != nil {
		return domain.TapeLabel{}, err
	}
	if formatted && !force {
		return domain.TapeLabel{}, &domain.AlreadyFormattedError{Name: old.Name, UUID: old.UUID}
	}

	uuid, err := uc.rand.UUID4()
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("format: генерация UUID: %w", err)
	}
	now := uc.clock.Now()
	label := domain.TapeLabel{
		Magic:         domain.Magic,
		FormatVersion: domain.FormatVersion,
		Name:          name,
		UUID:          uuid,
		FormattedAt:   now.UTC().Format(time.RFC3339),
	}
	if err := uc.writeLabel(ctx, label); err != nil {
		return domain.TapeLabel{}, err
	}
	if err := uc.cat.RegisterTape(ctx, label.UUID, label.Name, now.Unix()); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("format: регистрация кассеты: %w", err)
	}
	uc.log.Info("tape formatted",
		slog.String("name", label.Name),
		slog.String("uuid", label.UUID),
		slog.Bool("force", force))
	return label, nil
}

// readLabel читает ярлык с BOT. formatted=false для пустой ленты и
// чужого формата; NewerFormatError пробрасывается.
func (uc *UseCase) readLabel(ctx context.Context) (domain.TapeLabel, bool, error) {
	block, err := uc.tape.ReadBlock(ctx)
	if errors.Is(err, io.EOF) {
		return domain.TapeLabel{}, false, nil
	}
	if err != nil {
		return domain.TapeLabel{}, false, fmt.Errorf("format: чтение ярлыка: %w", err)
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		var foreign *domain.ForeignFormatError
		var blank *domain.BlankTapeError
		if errors.As(err, &foreign) || errors.As(err, &blank) {
			return domain.TapeLabel{}, false, nil
		}
		return domain.TapeLabel{}, false, fmt.Errorf("format: разбор ярлыка: %w", err)
	}
	return label, true, nil
}

// writeLabel перематывает в начало и пишет ярлык + двойной EOF
// (пустая лента: EOD сразу после ярлыка, docs/FORMAT.md §4).
func (uc *UseCase) writeLabel(ctx context.Context, label domain.TapeLabel) error {
	block, err := uc.codec.EncodeLabel(label)
	if err != nil {
		return fmt.Errorf("format: кодирование ярлыка: %w", err)
	}
	if err := uc.tape.Rewind(ctx); err != nil {
		return fmt.Errorf("format: перемотка перед записью: %w", err)
	}
	if err := uc.tape.WriteBlock(ctx, block); err != nil {
		return fmt.Errorf("format: запись ярлыка: %w", err)
	}
	if err := uc.tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("format: filemark после ярлыка: %w", err)
	}
	if err := uc.tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("format: второй filemark (EOD): %w", err)
	}
	return nil
}
