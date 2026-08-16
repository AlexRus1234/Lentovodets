// Фабрика slog-логгера с уровнем из конфига. См. docs/ARCHITECTURE.md §6.4.

package sloglog

import (
	"io"
	"log/slog"
	"strings"
)

// New создаёт логгер с text-обработчиком, пишущим в w. Уровень —
// debug|info|warn|error (регистр не важен); любое другое значение — info.
func New(level string, w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: ParseLevel(level),
	}))
}

// ParseLevel переводит имя уровня из конфига в slog.Level;
// неизвестное имя — LevelInfo (SPECIFICATION §8: log_level = "info").
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
