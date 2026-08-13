// Package restore реализует use case восстановления с ленты.
//
// Три варианта (см. docs/SPECIFICATION.md §4.3), все проверяют xxhash:
//
//   - RestoreFull      — перемотка в начало, чтение всех сессий;
//   - RestoreSelective — MTFSF(2*K-1) к сессии K, выбор путей;
//   - RestoreSmart     — Catalog.GetAllFileCopies(path), перебор копий
//     до первой здоровой; иначе ErrNoHealthyCopy.
package restore
