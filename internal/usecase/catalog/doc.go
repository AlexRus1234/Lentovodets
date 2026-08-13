// Package catalog реализует use case работы с каталогом.
//
// Обёртка над port.Catalog без ленточной логики (docs/SPECIFICATION.md §4.4):
//
//   - ListTapes / ListSessions(tapeUUID?) / GetFiles(sessionID);
//   - Search(pattern) — глобальный glob-поиск файлов;
//   - DeleteSession(sessionID) — удаление записи из каталога
//     (каскадом через ON DELETE CASCADE; данные на ленте остаются);
//   - Prune(before) — удаление сессий старше before.
package catalog
