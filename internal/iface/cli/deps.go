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
	Copies(ctx context.Context, path string) ([]port.FileCopy, error)
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

	// IsInteractive сообщает, доступен ли интерактивный ввод
	// (stdin — терминал): решает, можно ли задать вопрос оператору
	// (смена кассеты spanning). nil — всегда false.
	IsInteractive func() bool
}
