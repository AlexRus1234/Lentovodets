// Типизированные ошибки домена. См. docs/ARCHITECTURE.md §6.1–6.3.
//
// Ошибки — структуры с полями-деталями; sentinel-переменных намеренно
// нет (в проекте запрещены package-level var). Сравнение:
//
//	var tf *domain.TapeFullError
//	if errors.As(err, &tf) { ... }
//
// или
//
//	if errors.Is(err, &domain.TapeFullError{}) { ... }

package domain

import "fmt"

// TapeFullError — место на ленте закончилось во время записи.
type TapeFullError struct {
	Written  int64 // байт записано в текущую сессию до отказа
	Capacity int64 // оценка ёмкости ленты в байтах; 0 — неизвестна
}

// Error реализует интерфейс error.
func (e *TapeFullError) Error() string {
	if e.Capacity > 0 {
		return fmt.Sprintf("лента заполнена: записано %d байт из ~%d", e.Written, e.Capacity)
	}
	return fmt.Sprintf("лента заполнена: записано %d байт", e.Written)
}

// Is поддерживает errors.Is(err, &TapeFullError{}).
func (e *TapeFullError) Is(target error) bool {
	_, ok := target.(*TapeFullError)
	return ok
}

// LabelMismatchError — ярлык на ленте не совпадает с каталогом
// (обычно по UUID).
type LabelMismatchError struct {
	Expected string
	Actual   string
}

// Error реализует интерфейс error.
func (e *LabelMismatchError) Error() string {
	return fmt.Sprintf("несовпадение ярлыка ленты: ожидался %q, на ленте %q", e.Expected, e.Actual)
}

// Is поддерживает errors.Is(err, &LabelMismatchError{}).
func (e *LabelMismatchError) Is(target error) bool {
	_, ok := target.(*LabelMismatchError)
	return ok
}

// ForeignFormatError — на ленте чужой формат: magic не наш и не
// начинается с "LENTOVODEC_TAPE_".
type ForeignFormatError struct {
	Magic string // магическая строка, найденная на ленте
}

// Error реализует интерфейс error.
func (e *ForeignFormatError) Error() string {
	return fmt.Sprintf("чужой формат ленты: magic %q", e.Magic)
}

// Is поддерживает errors.Is(err, &ForeignFormatError{}).
func (e *ForeignFormatError) Is(target error) bool {
	_, ok := target.(*ForeignFormatError)
	return ok
}

// BlankTapeError — лента пуста: ярлык не найден.
type BlankTapeError struct{}

// Error реализует интерфейс error.
func (e *BlankTapeError) Error() string { return "лента пуста: ярлык не найден" }

// Is поддерживает errors.Is(err, &BlankTapeError{}).
func (e *BlankTapeError) Is(target error) bool {
	_, ok := target.(*BlankTapeError)
	return ok
}

// NewerFormatError — версия формата на ленте новее поддерживаемой.
type NewerFormatError struct {
	Found     int // версия в ярлыке
	Supported int // версия, поддерживаемая бинарем
}

// Error реализует интерфейс error.
func (e *NewerFormatError) Error() string {
	return fmt.Sprintf("версия формата %d новее поддерживаемой %d", e.Found, e.Supported)
}

// Is поддерживает errors.Is(err, &NewerFormatError{}).
func (e *NewerFormatError) Is(target error) bool {
	_, ok := target.(*NewerFormatError)
	return ok
}

// SessionNotFoundError — сессии с таким ID нет в каталоге.
type SessionNotFoundError struct {
	SessionID int64
}

// Error реализует интерфейс error.
func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("сессия %d не найдена в каталоге", e.SessionID)
}

// Is поддерживает errors.Is(err, &SessionNotFoundError{}).
func (e *SessionNotFoundError) Is(target error) bool {
	_, ok := target.(*SessionNotFoundError)
	return ok
}

// NoHealthyCopyError — ни одна известная копия файла не читается
// (smart restore перебрал все копии).
type NoHealthyCopyError struct {
	Path string
}

// Error реализует интерфейс error.
func (e *NoHealthyCopyError) Error() string {
	return fmt.Sprintf("все известные копии %q повреждены или недоступны", e.Path)
}

// Is поддерживает errors.Is(err, &NoHealthyCopyError{}).
func (e *NoHealthyCopyError) Is(target error) bool {
	_, ok := target.(*NoHealthyCopyError)
	return ok
}

// AlreadyFormattedError — лента уже отформатирована, а флаг force
// не задан.
type AlreadyFormattedError struct {
	Name string
	UUID string
}

// Error реализует интерфейс error.
func (e *AlreadyFormattedError) Error() string {
	return fmt.Sprintf("лента уже отформатирована: %q (%s)", e.Name, e.UUID)
}

// Is поддерживает errors.Is(err, &AlreadyFormattedError{}).
func (e *AlreadyFormattedError) Is(target error) bool {
	_, ok := target.(*AlreadyFormattedError)
	return ok
}
