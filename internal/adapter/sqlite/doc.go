// Package sqlite реализует port.Catalog поверх modernc.org/sqlite.
//
// Без CGO (modernc.org/sqlite — чистый Go). Схема — docs/SPECIFICATION.md
// §3.1, единственный CREATE TABLE IF NOT EXISTS без миграционного движка.
// В конструкторе обязательно: PRAGMA foreign_keys = ON; PRAGMA journal_mode
// = WAL.
package sqlite
