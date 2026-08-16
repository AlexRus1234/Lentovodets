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
