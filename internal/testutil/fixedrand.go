// FixedRand — двойник port.Rand: раздаёт предзагруженные UUID.
// См. docs/TESTING.md §3.5.

package testutil

import "lentovodec/internal/port"

// fixedRand циклически раздаёт предзагруженные UUID; при пустом
// списке — нулевой UUID v4.
type fixedRand struct {
	uuids []string
	next  int
}

// FixedRand создаёт источник, возвращающий uuids по очереди;
// после исчерпания списка — снова с начала.
func FixedRand(uuids ...string) port.Rand {
	if len(uuids) == 0 {
		uuids = []string{"00000000-0000-4000-8000-000000000000"}
	}
	return &fixedRand{uuids: uuids}
}

// UUID4 возвращает очередной предзагруженный UUID.
func (r *fixedRand) UUID4() (string, error) {
	u := r.uuids[r.next%len(r.uuids)]
	r.next++
	return u, nil
}

// failingRand всегда возвращает ошибку.
type failingRand struct{ err error }

// FailingRand создаёт источник случайности, отбиваемый сбоем err.
func FailingRand(err error) port.Rand {
	return failingRand{err: err}
}

// UUID4 возвращает предзагруженную ошибку.
func (r failingRand) UUID4() (string, error) { return "", r.err }
