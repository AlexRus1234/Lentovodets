// Зависимости CLI: точки внедрения фейков в тестах и интерфейсы
// конфигурации/демона. Продакшн-реализации — в wire.go.

package cli

import (
	"context"
	"io"
	"log/slog"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// ConfigFile — конфигурация, доступная CLI: чтение, редактирование
// заданий и параметры web-доступа (для клиентских команд daemon-режима).
// Реализация — adapter/tomlconfig.Config.
type ConfigFile interface {
	port.ConfigSource
	port.ConfigEditor
	Bind() string
	APIKey() string
	WebUsername() string
}

// ServerClient — HTTP-клиент демона для команд daemon-режима
// (tape info/eject, catalog *). Реализация — client.go.
type ServerClient interface {
	TapeInfo(ctx context.Context) (domain.TapeInfo, error)
	Eject(ctx context.Context) error
	ListTapes(ctx context.Context) ([]port.TapeRecord, error)
	ListSessions(ctx context.Context, tapeUUID string) ([]domain.Session, error)
	SessionFiles(ctx context.Context, sessionID int64) ([]domain.FileMeta, error)
	Search(ctx context.Context, pattern string) ([]port.FileCopy, error)
	DeleteSession(ctx context.Context, sessionID int64) error
	Prune(ctx context.Context, days int64) (int64, error)
}

// DaemonOpts — всё, что нужно демону; собирается командой daemon
// и передаётся в Deps.NewDaemon.
type DaemonOpts struct {
	Version      string
	Config       ConfigFile // конфигурация (реализация обязана удовлетворять web.ServerConfig)
	Logger       *slog.Logger
	BindOverride string // "host:port" из флагов; "" — взять из конфига
	Catalog      port.Catalog
	OpenTape     func(device string) (port.Tape, error)
	FS           port.Filesystem
	Codec        port.TapeCodec
	Hasher       port.Hasher
	Rand         port.Rand
	Clock        port.Clock
}

// Daemon — собранный HTTP-демон; запускается методом Run.
type Daemon interface {
	BindAddr() string
	Run(ctx context.Context) error
}

// Deps — внешние зависимости команды; DefaultDeps собирает продакшн,
// тесты подставляют фейки из internal/testutil.
type Deps struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Version string

	// OpenConfig открывает конфигурацию: путь + выставленные флаги
	// (ключ → значение; слой поверх env).
	OpenConfig func(path string, flags map[string]string) (ConfigFile, error)

	// OpenTape открывает ленту; probe с дружелюбной ошибкой (rootless,
	// SPEC §9.1) входит в реализацию из wire.
	OpenTape func(device string) (port.Tape, error)

	// OpenCatalog открывает каталог SQLite.
	OpenCatalog func(path string) (port.Catalog, error)

	FS     port.Filesystem
	Codec  port.TapeCodec
	Hasher port.Hasher
	Rand   port.Rand
	Clock  port.Clock

	// DialServer — HTTP-клиент демона для daemon-команд.
	DialServer func(url, apiKey, username string, askPassword func() (string, error)) ServerClient

	// NewDaemon собирает демона из опций (продакшн — web.Server).
	NewDaemon func(opts DaemonOpts) (Daemon, error)

	// AskPassword — интерактивный ввод пароля для daemon-логина.
	AskPassword func() (string, error)
}
