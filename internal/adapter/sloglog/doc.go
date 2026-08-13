// Package sloglog — тонкая обёртка над log/slog.
//
// Фабрика логгера с уровнем из конфига и структурными полями по умолчанию
// (docs/ARCHITECTURE.md §6.4). Все use case и адаптеры принимают
// *slog.Logger аргументом конструктора.
package sloglog
