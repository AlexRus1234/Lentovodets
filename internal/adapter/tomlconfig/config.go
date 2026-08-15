// Реализация port.ConfigSource на viper. См. docs/SPECIFICATION.md §8
// (ключи, слои применения) и §5 (значения по умолчанию).

package tomlconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/spf13/viper"

	"lentovodec/internal/domain"
)

// Ключи конфигурации. Регистр не важен при чтении (viper insensitive);
// env-переменная для ключа k — LENTOVODEC_<K в верхнем регистре>.
const (
	keyDevice   = "device"
	keyDB       = "db"
	keyLog      = "log"
	keyServer   = "server"
	keyLogLevel = "log_level"
	keyJobs     = "jobs"
)

// Значения по умолчанию (SPECIFICATION §5, §8).
const (
	defaultDevice   = "/dev/nst0"
	defaultDB       = "lentovodec.db"
	defaultLog      = "lentovodec.log"
	defaultServer   = "http://127.0.0.1:29201"
	defaultLogLevel = "info"
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

// Jobs — задания бекапа из секции [[jobs]].
func (c *Config) Jobs() ([]domain.Job, error) {
	var jobs []domain.Job
	if err := c.read.UnmarshalKey(keyJobs, &jobs); err != nil {
		return nil, fmt.Errorf("tomlconfig: разбор секции [[jobs]]: %w", err)
	}
	return jobs, nil
}
