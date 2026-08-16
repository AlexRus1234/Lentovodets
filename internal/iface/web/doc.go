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

// Package web — слой доставки HTTP на go-chi/chi/v5.
//
// Роутер и обработчики по группам (docs/SPECIFICATION.md §6):
//
//   - /api/status, /api/config, /api/settings;
//   - /api/tape/{info,eject,format};
//   - /api/jobs;
//   - /api/backup/start, /api/restore/start, /api/tasks/*;
//   - /api/catalog/*;
//   - статические ассеты Vue-бандла из embed.FS.
//
// TaskRegistry — in-memory map[TaskID]*Task с фоновыми goroutine для
// backup/restore; не персистентен (docs/ARCHITECTURE.md §5).
package web
