// HashFunc — функциональный двойник port.Hasher для тестов.

package testutil

import "io"

// HashFunc адаптирует функцию под port.Hasher.
type HashFunc func(r io.Reader) (string, error)

// Hash вызывает обёрнутую функцию.
func (f HashFunc) Hash(r io.Reader) (string, error) { return f(r) }
