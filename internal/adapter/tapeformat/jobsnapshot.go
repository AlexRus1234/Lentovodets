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

// Снапшот конфигурации задания в индексе сессии (docs/FORMAT.md §5):
// resolved доменное задание на момент записи сессии.

package tapeformat

import (
	"lentovodec/internal/domain"
)

// JobSnapshot — wire-вид снапшота конфигурации задания: зеркалит
// поля domain.Job. Отдельный тип держит JSON-представление
// стабильным при эволюции домена: новые поля domain не меняют
// записанные ранее снапшоты.
type JobSnapshot struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Mode        string   `json:"mode"`
	Paths       []string `json:"paths"`
	Exclude     []string `json:"exclude,omitempty"`
	SpanDepth   int32    `json:"span_depth,omitempty"`
}

// snapshotOf переводит доменное задание в wire-вид; nil → nil.
func snapshotOf(job *domain.Job) *JobSnapshot {
	if job == nil {
		return nil
	}
	return &JobSnapshot{
		Name:        job.Name,
		Description: job.Description,
		Mode:        string(job.Mode),
		Paths:       job.Paths,
		Exclude:     job.Exclude,
		SpanDepth:   job.SpanDepth,
	}
}

// job восстанавливает доменное задание из wire-вида; nil → nil
// (метод вызывается и на nil-приёмнике — дедуп-сессии без тела).
func (s *JobSnapshot) job() *domain.Job {
	if s == nil {
		return nil
	}
	return &domain.Job{
		Name:        s.Name,
		Description: s.Description,
		Mode:        domain.JobMode(s.Mode),
		Paths:       s.Paths,
		Exclude:     s.Exclude,
		SpanDepth:   s.SpanDepth,
	}
}
