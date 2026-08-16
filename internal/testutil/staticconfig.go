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

// StaticConfig — двойник port.ConfigSource с фиксированным списком
// заданий; остальные геттеры возвращают нулевые значения. Нужен
// интеграционным и hardware-тестам, собирающим реальный backup.UseCase.
// См. docs/TESTING.md §3.6.

package testutil

import "lentovodec/internal/domain"

// StaticConfig отдаёт предзагруженные задания.
type StaticConfig struct {
	JobList []domain.Job

	// CapacityBytes/MinTailBytes — параметры планировщика spanning
	// (port.ConfigSource.Capacity/MinTail); нули — spanning выключен.
	CapacityBytes int64
	MinTailBytes  int64
}

// Jobs возвращает предзагруженный список заданий.
func (c *StaticConfig) Jobs() ([]domain.Job, error) { return c.JobList, nil }

// Device — путь к устройству ленты (не используется двойником).
func (c *StaticConfig) Device() string { return "" }

// DB — путь к каталогу (не используется двойником).
func (c *StaticConfig) DB() string { return "" }

// Log — путь к логу (не используется двойником).
func (c *StaticConfig) Log() string { return "" }

// Server — адрес демона (не используется двойником).
func (c *StaticConfig) Server() string { return "" }

// LogLevel — уровень логирования.
func (c *StaticConfig) LogLevel() string { return "info" }

// Capacity — ёмкость кассеты для планировщика spanning (0 — выключен).
func (c *StaticConfig) Capacity() (int64, error) { return c.CapacityBytes, nil }

// MinTail — порог остатка кассеты (0 — дефолт планировщика).
func (c *StaticConfig) MinTail() (int64, error) { return c.MinTailBytes, nil }
