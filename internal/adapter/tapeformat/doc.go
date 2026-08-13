// Package tapeformat реализует чистый формат ленты (без ввода-вывода).
//
// Здесь живут:
//
//   - encoder: WriteSession(tape, files, fs, prog) — JSON-индекс (добитый
//     до BlockSize), WriteEOF, tar-поток, WriteEOF;
//   - decoder: ReadSession(tape, dest, prog) ([]FileMeta, error) — чтение
//     индекса, затем tar с обязательной проверкой xxhash;
//   - label: EncodeLabel(label) []byte / DecodeLabel(block) (TapeLabel, error).
//
// Канон формата — docs/FORMAT.md. Блочный I/O делегируется в port.Tape,
// поэтому пакет тестируется на FakeTape с круглым round-trip и golden-файлами.
// Целевое покрытие: 100%.
package tapeformat
