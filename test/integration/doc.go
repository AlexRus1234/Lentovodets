// Package integration содержит интеграционные тесты без железа.
//
// Сценарии (docs/ROADMAP.md Этап 9):
//
//   - format -> backup -> eject-simulated -> read label -> restore full
//     (сравнение дерева файлов) через filetape + osfs + sqlite;
//   - mirror: создание/изменение/удаление файлов, проверка состояний
//     после восстановления;
//   - smart restore: несколько копий, повреждённая копия, fallback.
package integration
