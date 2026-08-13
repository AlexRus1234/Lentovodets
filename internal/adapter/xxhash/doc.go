// Package xxhash оборачивает xxhash64 (hex, 16 символов) под нужды
// tapeformat и scan. Используется для контроля целостности файлов:
// hex(xxhash64(content)) хранится в FileMeta.Hash и проверяется при любом
// restore (docs/FORMAT.md §8).
package xxhash
