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

// NoopLogger — *slog.Logger, отбрасывающий всё.

package testutil

import (
	"context"
	"log/slog"
)

// nopHandler — slog.Handler, не пишущий никуда.
type nopHandler struct{}

// Enabled сообщает, что уровень никогда не активен.
func (nopHandler) Enabled(context.Context, slog.Level) bool { return false }

// Handle отбрасывает запись.
func (nopHandler) Handle(context.Context, slog.Record) error { return nil }

// WithAttrs возвращает тот же обработчик.
func (h nopHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup возвращает тот же обработчик.
func (h nopHandler) WithGroup(string) slog.Handler { return h }

// NoopLogger создаёт логгер, отбрасывающий все записи.
func NoopLogger() *slog.Logger {
	return slog.New(nopHandler{})
}
