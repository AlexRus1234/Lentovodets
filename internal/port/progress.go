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

// Порт прогресса длительных операций. См. docs/SPECIFICATION.md §9
// (throttle 2 Гц) и §6.4 (объект прогресса).

package port

// Фазы длительных операций (ProgressUpdate.Phase); совпадают со
// значениями phase в REST-прогрессе.
const (
	PhaseScan       = "scan"        // сканирование ФС
	PhaseWrite      = "write"       // запись на ленту / чтение с ленты
	PhaseFinalize   = "finalize"    // фиксация каталога и filemark'ов
	PhaseTapeChange = "tape_change" // ожидание смены кассеты (spanning)
)

// ProgressUpdate — снимок прогресса длительной операции.
type ProgressUpdate struct {
	Phase          string // PhaseScan | PhaseWrite | PhaseFinalize | PhaseTapeChange
	CurrentFile    string // обрабатываемый файл; "" — не применимо
	ProcessedBytes int64  // обработано байт
	TotalBytes     int64  // всего байт; 0 — неизвестно

	// Message — текст для оператора (например, просьба вставить
	// следующую кассету); "" — не применимо.
	Message string
}

// ProgressReporter — приёмник обновлений прогресса.
//
// Реализация обязана троттлить доставку Update до 2 Гц (не чаще
// одного обновления в 500 мс); Done и Fail доставляются немедленно.
// Методы не возвращают ошибок и не должны блокировать вызывающего.
type ProgressReporter interface {
	// Update публикует очередной снимок прогресса.
	Update(u ProgressUpdate)

	// Done фиксирует успешное завершение операции.
	Done()

	// Fail фиксирует завершение операции ошибкой.
	Fail(err error)
}
