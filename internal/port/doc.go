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

// Package port описывает интерфейсы (порты) в терминах domain.
//
// Порты — это граница между бизнес-логикой и внешним миром. Конкретные
// реализации живут в internal/adapter и НЕ импортируются из domain/usecase
// (проверяется depguard-правилом no-adapter-in-core).
//
// Канонические порты (см. docs/SPECIFICATION.md и docs/ARCHITECTURE.md §3):
//
//   - Tape           — блочный I/O ленты + filemark'и + перемотки;
//   - Filesystem     — Walker/FileReader/FileWriter/Stater/Entry;
//   - Catalog        — CRUD над tapes/sessions/files в SQLite;
//   - ProgressReporter — throttle 2 Гц;
//   - Clock          — тестируемое время (замена time.Now в domain/usecase);
//   - Rand           — тестируемая случайность (UUID4 и т.п.);
//   - ConfigSource   — источник конфигурации (TOML/env/flags).
//
// Сам пакет port не тестируется: это только контракты.
package port
