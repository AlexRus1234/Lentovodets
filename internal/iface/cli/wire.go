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

// Продакшн-wiring: сборка адаптеров и use case'ов для main.
// См. docs/ROADMAP.md Этап 7, docs/ARCHITECTURE.md §3.

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"lentovodec/internal/adapter/osfs"
	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/adapter/tomlconfig"
	"lentovodec/internal/adapter/xxhash"
	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
)

// systemClock — port.Clock на time.Now (системные адаптеры живут
// в wire: тесты подменяют их через Deps).
type systemClock struct{}

// Now возвращает текущее время.
func (systemClock) Now() time.Time { return time.Now() }

// uuidRand — port.Rand на google/uuid.
type uuidRand struct{}

// UUID4 генерирует канонический UUID v4.
func (uuidRand) UUID4() (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("rand: генерация UUID: %w", err)
	}
	return u.String(), nil
}

// DefaultDeps собирает продакшн-зависимости CLI (адаптеры + демона).
func DefaultDeps(version string) Deps {
	return Deps{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Stdin:   os.Stdin,
		Version: version,

		OpenConfig: func(path string, flags map[string]string) (ConfigFile, error) {
			return tomlconfig.New(path, flags)
		},
		OpenTape:    openTapeDevice,
		OpenCatalog: func(path string) (port.Catalog, error) { return sqlite.New(path) },

		FS:     osfs.New(),
		Codec:  tapeformat.NewCodec(),
		Hasher: xxhash.New(),
		Rand:   uuidRand{},
		Clock:  systemClock{},

		DialServer: Dial,
		NewDaemon:  newWebDaemon,
		AskPassword: func() (string, error) {
			fmt.Fprint(os.Stderr, "пароль демона: ")
			return readLine(bufio.NewScanner(os.Stdin))
		},
		IsInteractive: func() bool {
			fi, err := os.Stdin.Stat()
			return err == nil && fi.Mode()&os.ModeCharDevice != 0
		},
	}
}

// newWebDaemon собирает HTTP-демона поверх iface/web и открытого
// каталога; после остановки HTTP каталог закрывается.
func newWebDaemon(opts DaemonOpts) (Daemon, error) {
	cfg, ok := opts.Config.(web.ServerConfig)
	if !ok {
		return nil, errors.New("cli: конфигурация не удовлетворяет web.ServerConfig")
	}
	srv, err := web.NewServer(web.Deps{
		Log:          opts.Logger,
		Version:      opts.Version,
		Config:       cfg,
		Editor:       opts.Config,
		Catalog:      opts.Catalog,
		FS:           opts.FS,
		Codec:        opts.Codec,
		Hasher:       opts.Hasher,
		Rand:         opts.Rand,
		Clock:        opts.Clock,
		OpenTape:     opts.OpenTape,
		BindOverride: opts.BindOverride,
	})
	if err != nil {
		return nil, err
	}
	return &webDaemon{srv: srv, cat: opts.Catalog}, nil
}

// webDaemon — Daemon поверх web.Server; владеет каталогом демона.
type webDaemon struct {
	srv *web.Server
	cat port.Catalog
}

// BindAddr — фактический адрес слушания.
func (d *webDaemon) BindAddr() string { return d.srv.BindAddr() }

// Run слушает до отмены контекста, затем закрывает каталог.
func (d *webDaemon) Run(ctx context.Context) error {
	err := d.srv.Run(ctx)
	if cerr := d.cat.Close(); err == nil {
		err = cerr
	}
	return err
}

// friendlyTapeError превращает EACCES/ENOENT в подсказку rootless-модели
// (SPEC §9.1): критерий доступа — probe устройства, не uid.
func friendlyTapeError(err error, device string) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf(
			"нет доступа к устройству %s (%w)\nподсказка: добавьте пользователя в группу tape (usermod -aG tape $USER) и перелогиньтесь",
			device, err)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf(
			"устройство %s не найдено (%w)\nподсказка: проверьте, что стример подключён и устройство существует (ls /dev/nst*)",
			device, err)
	default:
		return err
	}
}

// probeDevicePath запрещает молчаливое создание «лент» в /dev: путь
// под /dev обязан существовать заранее.
func probeDevicePath(device string) error {
	if !strings.HasPrefix(device, "/dev/") {
		return nil
	}
	if _, err := os.Stat(device); err != nil {
		return friendlyTapeError(err, device)
	}
	return nil
}
