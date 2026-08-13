// Package tomlconfig реализует port.ConfigSource на viper.
//
// Слои применения (см. docs/SPECIFICATION.md §8):
//
//	defaults (в коде) < lentovodec.toml < env LENTOVODEC_* < флаги CLI.
//
// Конфиг всегда TOML, даже если флаг --config указывает на .yaml/.json
// (фикс легаси-бага, docs/LEGACY_REFERENCE.md §2.2). Здесь же — AddJob /
// RemoveJob с записью обратно в TOML.
package tomlconfig
