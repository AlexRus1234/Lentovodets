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

// Package sqlite реализует port.Catalog поверх modernc.org/sqlite.
//
// Без CGO (modernc.org/sqlite — чистый Go). Схема — docs/SPECIFICATION.md
// §3.1; версии схемы — PRAGMA user_version, миграции применяются в
// конструкторе (migrations.go), свежая БД создаётся сразу актуальной.
// В конструкторе обязательно: PRAGMA foreign_keys = ON; PRAGMA journal_mode
// = WAL.
package sqlite
