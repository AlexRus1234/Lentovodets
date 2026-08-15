// FixedClock — двойник port.Clock: всегда одно и то же время.
// См. docs/TESTING.md §3.5.

package testutil

import (
	"time"

	"lentovodec/internal/port"
)

// fixedClock — реализация port.Clock на зафиксированном моменте.
type fixedClock struct{ t time.Time }

// FixedClock создаёт часы, замороженные на t.
func FixedClock(t time.Time) port.Clock {
	return fixedClock{t: t}
}

// Now возвращает зафиксированное время.
func (c fixedClock) Now() time.Time { return c.t }
