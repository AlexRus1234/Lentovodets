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

// Реализация port.ConfigSource на viper. См. docs/SPECIFICATION.md §8
// (ключи, слои применения) и §5 (значения по умолчанию).

package tomlconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"

	"lentovodec/internal/domain"
)

// Ключи конфигурации. Регистр не важен при чтении (viper insensitive);
// env-переменная для ключа k — LENTOVODEC_<K в верхнем регистре>.
const (
	keyDevice          = "device"
	keyDB              = "db"
	keyLog             = "log"
	keyServer          = "server"
	keyLogLevel        = "log_level"
	keyJobs            = "jobs"
	keyBind            = "bind"
	keyWebUsername     = "web_username"
	keyWebPasswordHash = "web_password_hash"
	keyAPIKey          = "api_key"
	keySessionTTL      = "session_ttl"
)

// Значения по умолчанию (SPECIFICATION §5, §8).
const (
	defaultDevice     = "/dev/nst0"
	defaultDB         = "lentovodec.db"
	defaultLog        = "lentovodec.log"
	defaultServer     = "http://127.0.0.1:29201"
	defaultLogLevel   = "info"
	defaultBind       = "127.0.0.1:29201"
	defaultSessionTTL = "72h"
)

// Config реализует port.ConfigSource (чтение) и port.ConfigEditor
// (запись заданий) поверх lentovodec.toml.
//
// Два экземпляра viper: read — слоёное представление для геттеров
// (defaults → TOML → env LENTOVODEC_* → флаги), file — только
// содержимое TOML-файла. Запись AddJob/RemoveJob идёт через file,
// поэтому значения из env и флагов в файл не протекают (секреты и
// путь устройства пользователя остаются как были).
type Config struct {
	read *viper.Viper
	file *viper.Viper
	path string
}

// New читает конфигурацию из path (всегда TOML — SPECIFICATION §8 и
// docs/ARCHITECTURE.md §6.5). Отсутствующий файл — не ошибка: работают
// defaults/env/флаги. flagOverrides — значения выставленных флагов CLI
// (ключ → значение; пустые строки игнорируются), наивысший приоритет.
func New(path string, flagOverrides map[string]string) (*Config, error) {
	file := viper.New()
	file.SetConfigFile(path)
	file.SetConfigType("toml")
	if err := readTolerantMissing(file, path); err != nil {
		return nil, err
	}

	read := viper.New()
	setDefaults(read)
	read.SetConfigFile(path)
	read.SetConfigType("toml")
	if err := readTolerantMissing(read, path); err != nil {
		return nil, err
	}
	read.SetEnvPrefix("LENTOVODEC")
	read.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	read.AutomaticEnv()
	for key, val := range flagOverrides {
		if val != "" {
			read.Set(key, val)
		}
	}
	return &Config{read: read, file: file, path: path}, nil
}

// readTolerantMissing читает TOML-файл в v; отсутствующий файл — не ошибка.
func readTolerantMissing(v *viper.Viper, path string) error {
	if err := v.ReadInConfig(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("tomlconfig: чтение %s: %w", path, err)
	}
	return nil
}

// setDefaults задаёт значения по умолчанию (нижний слой).
func setDefaults(v *viper.Viper) {
	v.SetDefault(keyDevice, defaultDevice)
	v.SetDefault(keyDB, defaultDB)
	v.SetDefault(keyLog, defaultLog)
	v.SetDefault(keyServer, defaultServer)
	v.SetDefault(keyLogLevel, defaultLogLevel)
	v.SetDefault(keyBind, defaultBind)
	v.SetDefault(keyWebUsername, "")
	v.SetDefault(keySessionTTL, defaultSessionTTL)
}

// Device — путь к устройству ленты.
func (c *Config) Device() string { return c.read.GetString(keyDevice) }

// DB — путь к SQLite-каталогу.
func (c *Config) DB() string { return c.read.GetString(keyDB) }

// Log — путь к файлу лога.
func (c *Config) Log() string { return c.read.GetString(keyLog) }

// Server — адрес демона для клиентских команд.
func (c *Config) Server() string { return c.read.GetString(keyServer) }

// LogLevel — уровень логирования: debug|info|warn|error.
func (c *Config) LogLevel() string { return c.read.GetString(keyLogLevel) }

// Bind — адрес, на котором слушает демон (SPECIFICATION §8, §9.2).
func (c *Config) Bind() string { return c.read.GetString(keyBind) }

// WebUsername — единственная учётка демона; "" — аутентификация
// выключена (разрешено только при bind на loopback).
func (c *Config) WebUsername() string { return c.read.GetString(keyWebUsername) }

// WebPasswordHash — bcrypt-хеш пароля демона. Читается только из TOML
// (file-viper без env-слоя): секреты через env не передаются
// (SPECIFICATION §8).
func (c *Config) WebPasswordHash() string { return c.file.GetString(keyWebPasswordHash) }

// APIKey — ключ для скриптов (X-API-Key); "" — отключён. Как и пароль,
// только из TOML (SPECIFICATION §8).
func (c *Config) APIKey() string { return c.file.GetString(keyAPIKey) }

// SessionTTL — время жизни сессий логина; некорректное значение
// молча заменяется на дефолт 72h.
func (c *Config) SessionTTL() time.Duration {
	d, err := time.ParseDuration(c.read.GetString(keySessionTTL))
	if err != nil || d <= 0 {
		return 72 * time.Hour
	}
	return d
}

// RawTOML — содержимое конфигурационного файла как текст (для
// GET /api/config). Отсутствующий файл — пустая строка без ошибки.
func (c *Config) RawTOML() (string, error) {
	raw, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("tomlconfig: чтение %s: %w", c.path, err)
	}
	return string(raw), nil
}

// Jobs — задания бекапа из секции [[jobs]].
func (c *Config) Jobs() ([]domain.Job, error) {
	var jobs []domain.Job
	if err := c.read.UnmarshalKey(keyJobs, &jobs); err != nil {
		return nil, fmt.Errorf("tomlconfig: разбор секции [[jobs]]: %w", err)
	}
	return jobs, nil
}
