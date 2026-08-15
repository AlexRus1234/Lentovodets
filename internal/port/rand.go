// Порт случайности. См. docs/ARCHITECTURE.md §6.2 (детерминированность).

package port

// Rand — источник случайности; заменяемая альтернатива rand.*
// внутри domain/usecase.
type Rand interface {
	// UUID4 возвращает UUID версии 4 (RFC-4122) в каноническом
	// виде: 8-4-4-4-12, lowercase.
	UUID4() (string, error)
}
