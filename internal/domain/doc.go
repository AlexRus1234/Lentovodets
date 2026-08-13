// Package domain содержит ЧИСТЫЕ доменные типы и функции.
//
// Слой domain:
//   - не имеет внешних зависимостей кроме стандартной библиотеки;
//   - не импортирует os/syscall/net/golang.org/x/sys (проверяется depguard);
//   - не выполняет никакого ввода-вывода.
//
// Канонические типы (см. docs/SPECIFICATION.md):
//
//   - Job, JobMode          — конфигурация задания бекапа;
//   - FileMeta, FileState   — метаданные файла и переходы состояний;
//   - Session, SessionType  — одна запись бекапа на ленте;
//   - TapeLabel, TapeInfo   — JSON-ярлык кассеты и константы формата;
//   - фильтры (glob/doublestar exclude) и нормализация путей;
//   - типизированные ошибки: ErrTapeFull, ErrLabelMismatch, ...
//
// Целевое покрытие тестами: 100%.
package domain
