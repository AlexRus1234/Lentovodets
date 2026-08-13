// Package backup реализует use case резервного копирования на ленту.
//
// Алгоритм — в docs/ARCHITECTURE.md §4.1:
// загрузка Job → чтение TapeLabel → определение sessionNum и Full/Inc →
// Scanner.Scan → Catalog.CreateSession → Tape.Rewind/LocateEOD →
// tapeformat.WriteSession → Catalog.SaveFiles.
//
// Опции: Full (полный бекап), DryRun (только сканирование).
// Возвращает Session и Stats{Scanned, Added, Modified, Deleted, Bytes}.
package backup
