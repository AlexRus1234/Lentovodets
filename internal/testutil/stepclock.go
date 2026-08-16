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
