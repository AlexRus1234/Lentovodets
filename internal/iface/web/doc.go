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
