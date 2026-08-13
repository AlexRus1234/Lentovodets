// Package scan реализует use case сканирования файловой системы.
//
// Scanner сравнивает текущий снимок ФС (через port.Filesystem) с прошлым
// снимком из port.Catalog и для каждого пути вычисляет FileState:
//
//   - Added    — файла не было в прошлой сессии;
//   - Modified — изменился Size или ModTime (или Hash при full check);
//   - Deleted  — был в прошлой сессии, теперь отсутствует
//     (только для job.Mode == mirror; становится tombstone).
//
// Целевое покрытие: >= 95% (docs/TESTING.md).
package scan
