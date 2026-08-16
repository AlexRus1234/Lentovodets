// StaticConfig — двойник port.ConfigSource с фиксированным списком
// заданий; остальные геттеры возвращают нулевые значения. Нужен
// интеграционным и hardware-тестам, собирающим реальный backup.UseCase.
// См. docs/TESTING.md §3.6.

package testutil

import "lentovodec/internal/domain"

// StaticConfig отдаёт предзагруженные задания.
type StaticConfig struct {
	JobList []domain.Job
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
