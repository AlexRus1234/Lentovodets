// Package cli — слой доставки на cobra + viper.
//
// Одна подкоманда — один файл (docs/SPECIFICATION.md §5). Команды
// daemon-режима (catalog/*, tape info/eject) ходят в HTTP API демона через
// внутренний HTTPClient; локальные команды (backup, restore, tape format,
// jobs *) работают напрямую с лентой без демона.
//
// Тонкая обёртка: вся логика — в usecase. Здесь только парсинг флагов,
// DI-композиция и вывод.
package cli
