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
