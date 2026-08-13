// Package filetape реализует port.Tape поверх обычного файла.
//
// Назначение — dev/CI: эмулировать стример одним файлом. Хранит байты +
// slice позиций filemark'ов; семантика ForwardFilemarks/EndOfData повторяет
// MTFSF/MTEOM (docs/TESTING.md §3.1).
//
// Один файл — одна лента; для тестов создаётся в t.TempDir(). Это
// реализация "ленты как файла", а не тестовый двойник:FakeTape из testutil.
package filetape
