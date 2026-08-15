// NoProgress — заглушка port.ProgressReporter. См. docs/TESTING.md §3.4.

package testutil

import "lentovodec/internal/port"

// NoProgress отбрасывает все обновления прогресса.
type NoProgress struct{}

// Update отбрасывает снимок прогресса.
func (NoProgress) Update(port.ProgressUpdate) {}

// Done — no-op.
func (NoProgress) Done() {}

// Fail — no-op.
func (NoProgress) Fail(error) {}
