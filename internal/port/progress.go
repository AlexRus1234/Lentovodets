// Порт прогресса длительных операций. См. docs/SPECIFICATION.md §9
// (throttle 2 Гц) и §6.4 (объект прогресса).

package port

// Фазы длительных операций (ProgressUpdate.Phase); совпадают со
// значениями phase в REST-прогрессе.
const (
	PhaseScan     = "scan"     // сканирование ФС
	PhaseWrite    = "write"    // запись на ленту / чтение с ленты
	PhaseFinalize = "finalize" // фиксация каталога и filemark'ов
)

// ProgressUpdate — снимок прогресса длительной операции.
type ProgressUpdate struct {
	Phase          string // PhaseScan | PhaseWrite | PhaseFinalize
	CurrentFile    string // обрабатываемый файл; "" — не применимо
	ProcessedBytes int64  // обработано байт
	TotalBytes     int64  // всего байт; 0 — неизвестно
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
