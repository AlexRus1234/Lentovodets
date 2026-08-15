// Порт времени. См. docs/ARCHITECTURE.md §6.2 (детерминированность).

package port

import "time"

// Clock — источник текущего времени; единственная заменяемая
// альтернатива time.Now внутри domain/usecase.
type Clock interface {
	Now() time.Time
}
