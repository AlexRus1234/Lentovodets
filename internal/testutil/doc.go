// Package testutil содержит общие test doubles для юнит- и
// интеграционных тестов.
//
// Допустимое исключение из правила зависимостей: testutil сам зависит
// только от port/domain (docs/TESTING.md §3). Доступен из *_test.go в любых
// пакетах внутри internal/.
//
// Канонические двойники:
//
//   - FakeTape     — bytes.Buffer + slice of filemark positions;
//   - MapFS        — обёртка над testing/fstest.MapFS под port.Filesystem;
//   - MemCatalog   — полная in-memory реализация port.Catalog;
//   - NoProgress   — заглушка ProgressReporter;
//   - FixedClock   — Clock с предзагруженным Now;
//   - FixedRand    — Rand с предзагруженными UUID4;
//   - NoopLogger   — slog.Logger, никуда не пишущий.
package testutil
