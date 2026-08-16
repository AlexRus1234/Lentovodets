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
)

// UseCase — тонкая композиция портов: вся логика в адаптерах.
type UseCase struct {
	cat   port.Catalog
	tape  port.Tape
	codec port.TapeCodec
	prog  port.ProgressReporter // nil допустим
	log   *slog.Logger
}

// New собирает use case; tape/codec могут быть nil для чисто
// каталожных сценариев (daemon без устройства).
func New(
	cat port.Catalog,
	tape port.Tape,
	codec port.TapeCodec,
	prog port.ProgressReporter,
	log *slog.Logger,
) *UseCase {
	return &UseCase{cat: cat, tape: tape, codec: codec, prog: prog, log: log}
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
	block, err := uc.tape.ReadBlock(ctx)
	if err != nil {
		return domain.TapeInfo{}, fmt.Errorf("catalog: чтение ярлыка: %w", labelReadErr(err))
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeInfo{}, fmt.Errorf("catalog: разбор ярлыка: %w", err)
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

// ReadTest прогоняет всю ленту в режиме проверки: чтение всех сессий
// с сверкой хешей без записи на ФС. Возвращает число проверенных
// сессий; повреждённые — ошибка.
func (uc *UseCase) ReadTest(ctx context.Context) (int, error) {
	if err := uc.tape.Rewind(ctx); err != nil {
		return 0, fmt.Errorf("readtest: перемотка: %w", err)
	}
	if _, err := uc.readLabel(ctx); err != nil {
		return 0, err
	}
	// Позиция — на filemark'е ярлыка; переводим на индекс сессии 1
	// (FORMAT §9: чтение сессий подряд начинается после файла ярлыка).
	if err := uc.tape.ForwardFilemarks(ctx, 1); err != nil {
		return 0, fmt.Errorf("readtest: пропуск ярлыка: %w", err)
	}
	checked := 0
	for {
		if err := ctx.Err(); err != nil {
			return checked, fmt.Errorf("readtest: %w", err)
		}
		_, err := uc.codec.ReadSession(ctx, uc.tape, nil, uc.prog)
		if isEndOfSessions(err) {
			break
		}
		if err != nil {
			return checked, fmt.Errorf("readtest: сессия %d: %w", checked+1, err)
		}
		checked++
	}
	if uc.prog != nil {
		uc.prog.Done()
	}
	return checked, nil
}

// readLabel читает ярлык с BOT.
func (uc *UseCase) readLabel(ctx context.Context) (domain.TapeLabel, error) {
	block, err := uc.tape.ReadBlock(ctx)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("readtest: чтение ярлыка: %w", labelReadErr(err))
	}
	label, err := uc.codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("readtest: разбор ярлыка: %w", err)
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
