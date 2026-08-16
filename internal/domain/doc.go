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

// Package domain содержит ЧИСТЫЕ доменные типы и функции.
//
// Слой domain:
//   - не имеет внешних зависимостей кроме стандартной библиотеки;
//   - не импортирует os/syscall/net/golang.org/x/sys (проверяется depguard);
//   - не выполняет никакого ввода-вывода.
//
// Канонические типы (см. docs/SPECIFICATION.md):
//
//   - Job, JobMode          — конфигурация задания бекапа;
//   - FileMeta, FileState   — метаданные файла и переходы состояний;
//   - Session, SessionType  — одна запись бекапа на ленте;
//   - TapeLabel, TapeInfo   — JSON-ярлык кассеты и константы формата;
//   - фильтры (glob/doublestar exclude) и нормализация путей;
//   - типизированные ошибки: ErrTapeFull, ErrLabelMismatch, ...
//
// Целевое покрытие тестами: 100%.
package domain
