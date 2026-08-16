// StepClock — двойник port.Clock: каждый вызов Now возвращает время на
// шаг позже предыдущего. Даёт строго возрастающие метки времени
// (форматирование, сессии бекапа) в интеграционных тестах — в отличие
// от FixedClock, у которого все события происходят «одновременно».
// См. docs/TESTING.md §3.5.

package testutil

import (
	"sync"
	"time"

	"lentovodec/internal/port"
)

// stepClock раздаёт время с постоянным шагом.
type stepClock struct {
	mu   sync.Mutex
	next time.Time
	step time.Duration
}

// StepClock создаёт часы, идущие вперёд на 1 секунду за вызов Now.
func StepClock(start time.Time) port.Clock {
	return &stepClock{next: start, step: time.Second}
}

// Now возвращает очередное значение и сдвигает часы на шаг.
func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.next
	c.next = c.next.Add(c.step)
	return t
}
