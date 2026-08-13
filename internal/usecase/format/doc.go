// Package format реализует use case форматирования кассеты.
//
// Шаги (docs/ARCHITECTURE.md §4.3):
//
//  1. Tape.Rewind;
//  2. при !force — чтение существующего label, ErrAlreadyFormatted если есть;
//  3. label = {Magic, FormatVersion, Name, UUID=Rand.UUID4(),
//     FormattedAt=Clock.Now()};
//  4. WriteBlock(EncodeLabel(label));
//  5. WriteEOF;
//  6. WriteEOF (второй EOF = пустая лента с EOD сразу после ярлыка);
//  7. Catalog.RegisterTape(uuid, name).
//
// Двойной EOF после ярлыка — канонический EOD (docs/FORMAT.md §4).
package format
