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
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
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
	keyWebhookURL      = "webhook_url"
	keyWebhookTimeout  = "webhook_timeout"
	keyCapacity        = "capacity"
	keyMinTail         = "min_tail"
)

// Значения по умолчанию (SPECIFICATION §5, §8).
const (
	defaultDevice         = "/dev/nst0"
	defaultDB             = "lentovodec.db"
	defaultLog            = "lentovodec.log"
	defaultServer         = "http://127.0.0.1:29201"
	defaultLogLevel       = "info"
	defaultBind           = "127.0.0.1:29201"
	defaultSessionTTL     = "72h"
	defaultWebhookTimeout = "10s"
)

// Config реализует port.ConfigSource (чтение) и port.ConfigEditor
// (запись заданий) поверх lentovodec.toml.
//
// Два экземпляра viper: read — слоёное представление для геттеров
// (defaults → TOML → env LENTOVODEC_* → флаги), file — только
// содержимое TOML-файла. Запись AddJob/RemoveJob идёт через file,
// поэтому значения из env и флагов в файл не протекают (секреты и
// путь устройства пользователя остаются как были).
//
// Все методы под мьютексом: демон читает конфиг горутиной задачи
// (Capacity/MinTail/Jobs) параллельно с редактированием заданий из
// HTTP; viper не потокобезопасен.
type Config struct {
	mu   sync.Mutex
	read *viper.Viper
	file *viper.Viper
	path string
}

// New читает конфигурацию из path (всегда TOML — SPECIFICATION §8 и
// docs/ARCHITECTURE.md §6.5). Отсутствующий файл — не ошибка: работают
// defaults/env/флаги. flagOverrides — значения выставленных флагов CLI
// (ключ → значение; пустые строки игнорируются), наивысший приоритет.
// Файл читается с диска один раз: оба представления строятся из одних
// байт (нет окна TOCTOU между двумя чтениями).
func New(path string, flagOverrides map[string]string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("tomlconfig: чтение %s: %w", path, err)
	}
	file := viper.New()
	file.SetConfigType("toml")
	if len(raw) > 0 {
		if err := file.ReadConfig(bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("tomlconfig: разбор %s: %w", path, err)
		}
	}

	read := viper.New()
	setDefaults(read)
	read.SetConfigType("toml")
	if len(raw) > 0 {
		if err := read.ReadConfig(bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("tomlconfig: разбор %s: %w", path, err)
		}
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
	v.SetDefault(keyWebhookURL, "")
	v.SetDefault(keyWebhookTimeout, defaultWebhookTimeout)
	v.SetDefault(keyCapacity, "")
	v.SetDefault(keyMinTail, "")
}

// Device — путь к устройству ленты.
func (c *Config) Device() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyDevice)
}

// DB — путь к SQLite-каталогу.
func (c *Config) DB() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyDB)
}

// Log — путь к файлу лога.
func (c *Config) Log() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyLog)
}

// Server — адрес демона для клиентских команд.
func (c *Config) Server() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyServer)
}

// LogLevel — уровень логирования: debug|info|warn|error.
func (c *Config) LogLevel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyLogLevel)
}

// Bind — адрес, на котором слушает демон (SPECIFICATION §8, §9.2).
func (c *Config) Bind() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyBind)
}

// WebUsername — единственная учётка демона; "" — аутентификация
// выключена (разрешено только при bind на loopback).
func (c *Config) WebUsername() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read.GetString(keyWebUsername)
}

// WebPasswordHash — bcrypt-хеш пароля демона. Читается только из TOML
// (file-viper без env-слоя): секреты через env не передаются
// (SPECIFICATION §8).
func (c *Config) WebPasswordHash() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.GetString(keyWebPasswordHash)
}

// APIKey — ключ для скриптов (X-API-Key); "" — отключён. Как и пароль,
// только из TOML (SPECIFICATION §8).
func (c *Config) APIKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.GetString(keyAPIKey)
}

// SessionTTL — время жизни сессий логина; некорректное значение
// молча заменяется на дефолт 72h.
func (c *Config) SessionTTL() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := time.ParseDuration(c.read.GetString(keySessionTTL))
	if err != nil || d <= 0 {
		return 72 * time.Hour
	}
	return d
}

// WebhookURL — URL для уведомлений о завершённых задачах; пустой URL отключает их.
func (c *Config) WebhookURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.file.GetString(keyWebhookURL))
}

// WebhookTimeout — таймаут одного HTTP-запроса webhook.
func (c *Config) WebhookTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := time.ParseDuration(c.read.GetString(keyWebhookTimeout))
	if err != nil || d <= 0 {
		return 10 * time.Second
	}
	return d
}

// Capacity — оценка ёмкости кассеты в байтах для планировщика частей
// spanning (ключ capacity, человекочитаемая строка вида "2.2T";
// человекочитаемые строки разбирает domain.ParseSize). Ключ отсутствует
// или пуст — 0: spanning выключен, поведение одной кассеты. Битая
// строка — ошибка.
func (c *Config) Capacity() (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capacityLocked()
}

// capacityLocked читает capacity без захвата мьютекса (уже под
// блокировкой — вызывается из MinTail).
func (c *Config) capacityLocked() (int64, error) {
	raw := strings.TrimSpace(c.read.GetString(keyCapacity))
	if raw == "" {
		return 0, nil
	}
	n, err := domain.ParseSize(raw)
	if err != nil {
		return 0, fmt.Errorf("tomlconfig: ключ capacity: %w", err)
	}
	return n, nil
}

// MinTail — порог остатка текущей кассеты в байтах: остаток ниже
// порога — новая сессия начинается на новой кассете (ключ min_tail).
// Явное значение — любая строка domain.ParseSize; дефолт — 5%
// capacity, но не меньше одного блока ленты (domain.BlockSize);
// без capacity — 0.
func (c *Config) MinTail() (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw := strings.TrimSpace(c.read.GetString(keyMinTail))
	if raw != "" {
		n, err := domain.ParseSize(raw)
		if err != nil {
			return 0, fmt.Errorf("tomlconfig: ключ min_tail: %w", err)
		}
		return n, nil
	}
	capacity, err := c.capacityLocked()
	if err != nil || capacity == 0 {
		return 0, err
	}
	tail := capacity / 20
	if tail < domain.BlockSize {
		tail = domain.BlockSize
	}
	return tail, nil
}

// RawTOML — содержимое конфигурационного файла как текст (для
// GET /api/config). Отсутствующий файл — пустая строка без ошибки.
func (c *Config) RawTOML() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jobsLocked()
}

// jobsLocked читает задания без захвата мьютекса (уже под блокировкой).
func (c *Config) jobsLocked() ([]domain.Job, error) {
	var jobs []domain.Job
	if err := c.read.UnmarshalKey(keyJobs, &jobs); err != nil {
		return nil, fmt.Errorf("tomlconfig: разбор секции [[jobs]]: %w", err)
	}
	return jobs, nil
}
