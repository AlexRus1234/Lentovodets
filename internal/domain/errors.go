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

// TapeNotFoundError — кассеты с таким UUID нет в каталоге.
type TapeNotFoundError struct {
	UUID string
}

// Error реализует интерфейс error.
func (e *TapeNotFoundError) Error() string {
	return fmt.Sprintf("кассета %s не найдена в каталоге", e.UUID)
}

// Is поддерживает errors.Is(err, &TapeNotFoundError{}).
func (e *TapeNotFoundError) Is(target error) bool {
	_, ok := target.(*TapeNotFoundError)
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

// FileTooLargeError — одиночный файл не помещается в бюджет кассеты
// (остаток при дозаписи или всю ёмкость): разрез сессии идёт только
// по границам файлов, такой файл записать нельзя. Ловится планировщиком
// до записи.
type FileTooLargeError struct {
	Path     string
	Size     int64
	Capacity int64 // бюджет части, в который файл не влезает
}

// Error реализует интерфейс error.
func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf(
		"файл %s (%d байт) больше бюджета кассеты (%d байт)", e.Path, e.Size, e.Capacity)
}

// Is поддерживает errors.Is(err, &FileTooLargeError{}).
func (e *FileTooLargeError) Is(target error) bool {
	_, ok := target.(*FileTooLargeError)
	return ok
}

// TapeChangerError — планировщик разделил сессию на части по ёмкости,
// а смена кассет недоступна (нет интерактивного changer'а или демона).
// Каталог и лента не тронуты: ошибка возвращается до записи.
type TapeChangerError struct{}

// Error реализует интерфейс error.
func (e *TapeChangerError) Error() string {
	return "spanning требует интерактивной смены кассет или демона"
}

// Is поддерживает errors.Is(err, &TapeChangerError{}).
func (e *TapeChangerError) Is(target error) bool {
	_, ok := target.(*TapeChangerError)
	return ok
}

// EmptyIndexError — пустой индекс сессии при чтении ленты подряд:
// достигнут конец записанных сессий (EOD) либо повреждена граница.
type EmptyIndexError struct{}

// Error реализует интерфейс error.
func (e *EmptyIndexError) Error() string { return "индекс сессии пуст" }

// Is поддерживает errors.Is(err, &EmptyIndexError{}).
func (e *EmptyIndexError) Is(target error) bool {
	_, ok := target.(*EmptyIndexError)
	return ok
}

// ContinuationError — при последовательном чтении ленты на позиции
// сессии найден блок-указатель продолжения: кассета кончилась,
// цепочка сессий продолжается на следующей кассете. Поля — напрямую
// (domain не импортирует port; port.Continuation конвертируется в
// них адаптером).
type ContinuationError struct {
	JobRunID     string // UUID запуска, к которому относится цепочка
	SessionNum   int32  // номер сессии на ленте
	Part         int32  // номер части, продолжающейся на следующей кассете
	NextTapeName string // имя следующей кассеты цепочки
}

// Error реализует интерфейс error.
func (e *ContinuationError) Error() string {
	return fmt.Sprintf(
		"кассета имеет продолжение: сессия %d, часть %d — на кассете %q",
		e.SessionNum, e.Part, e.NextTapeName)
}

// Is поддерживает errors.Is(err, &ContinuationError{}).
func (e *ContinuationError) Is(target error) bool {
	_, ok := target.(*ContinuationError)
	return ok
}

// ChainMismatchError — вставленная кассета не подходит цепочке
// восстановления: имя ярлыка не совпало с ожидаемым из указателя
// продолжения либо обратная ссылка continues индекса первой сессии
// не совпала с UUID предыдущей кассеты. Защита от вставленной «не
// той» кассеты: ошибка возвращается сразу, до восстановления данных;
// повторный запуск после вставки верной кассеты восстановит
// пропущенное.
type ChainMismatchError struct {
	Expected string // ожидавшееся имя кассеты или UUID предыдущей
	Got      string // фактическое имя ярлыка или ссылка continues
}

// Error реализует интерфейс error.
func (e *ChainMismatchError) Error() string {
	return fmt.Sprintf("кассета не подходит для цепочки: ожидалось %q, найдено %q", e.Expected, e.Got)
}

// Is поддерживает errors.Is(err, &ChainMismatchError{}).
func (e *ChainMismatchError) Is(target error) bool {
	_, ok := target.(*ChainMismatchError)
	return ok
}

// NotContinuationError — блок на позиции указателя продолжения не
// является continuation-блоком: битый JSON или чужой блок без
// kind:"continuation".
type NotContinuationError struct {
	Snippet string // первые байты блока для диагностики
}

// Error реализует интерфейс error.
func (e *NotContinuationError) Error() string {
	return fmt.Sprintf("блок не является указателем продолжения: %q", e.Snippet)
}

// Is поддерживает errors.Is(err, &NotContinuationError{}).
func (e *NotContinuationError) Is(target error) bool {
	_, ok := target.(*NotContinuationError)
	return ok
}

// NoMediumError — в приводе нет кассеты: open или ioctl st-драйвера
// вернул ENOMEDIUM («no medium found»). iface-слои показывают текст
// ошибки вместо сырого errno (web: 409 code=no_medium).
type NoMediumError struct{}

// Error реализует интерфейс error.
func (e *NoMediumError) Error() string { return "нет кассеты в приводе" }

// Is поддерживает errors.Is(err, &NoMediumError{}).
func (e *NoMediumError) Is(target error) bool {
	_, ok := target.(*NoMediumError)
	return ok
}
