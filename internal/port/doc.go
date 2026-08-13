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
