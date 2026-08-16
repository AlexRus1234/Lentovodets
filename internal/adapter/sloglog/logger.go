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
