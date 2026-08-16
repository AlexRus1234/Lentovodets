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

// Порт конфигурации. См. docs/SPECIFICATION.md §5 (флаги) и §8 (TOML).
// Слои применения: defaults → TOML → env LENTOVODEC_* → флаги CLI.

package port

import "lentovodec/internal/domain"

// ConfigSource — чтение конфигурации после слияния слоёв.
type ConfigSource interface {
	// Jobs — задания бекапа из секции [[jobs]].
	Jobs() ([]domain.Job, error)

	// Device — путь к устройству ленты (по умолчанию /dev/nst0).
	Device() string

	// DB — путь к SQLite-каталогу (по умолчанию ./lentovodec.db).
	DB() string

	// Log — путь к файлу лога (по умолчанию ./lentovodec.log).
	Log() string

	// Server — адрес демона для клиентских команд
	// (по умолчанию http://127.0.0.1:29201).
	Server() string

	// LogLevel — уровень логирования: debug|info|warn|error.
	LogLevel() string
}

// ConfigEditor — изменение конфигурации: запись заданий обратно
// в конфигурационный файл (TOML); нужно iface/web и CLI jobs add/remove.
type ConfigEditor interface {
	// AddJob добавляет задание; дубликат имени — ошибка.
	AddJob(job domain.Job) error

	// RemoveJob удаляет задание по имени; отсутствие имени — ошибка.
	RemoveJob(name string) error
}
