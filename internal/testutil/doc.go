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
