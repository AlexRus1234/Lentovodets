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

// Сессия бекапа на ленте. См. docs/SPECIFICATION.md §2.6.

package domain

// SessionType — тип сессии.
type SessionType string

// Допустимые значения SessionType.
const (
	// SessionFull — полный бекап; первая сессия на ленте обязана быть FULL.
	SessionFull SessionType = "FULL"

	// SessionInc — инкрементальный бекап.
	SessionInc SessionType = "INC"
)

// Valid сообщает, что значение — один из допустимых типов.
func (t SessionType) Valid() bool {
	return t == SessionFull || t == SessionInc
}

// Session — один запуск бекапа, записанный на ленту. На одной ленте
// несколько сессий; нумерация в пределах ленты, начиная с 1.
type Session struct {
	ID        int64       // PK в таблице sessions
	TapeUUID  string      // FK на tapes.uuid
	Num       int32       // порядковый номер на этой ленте, начиная с 1
	Type      SessionType // FULL или INC
	Timestamp int64       // Unix-секунды старта сессии
	JobRunID  string      // UUID запуска; группирует части одного бекапа

	// Part — номер части в цепочке spanning-запуска, начиная с 1.
	// Обычная (не разделённая) сессия — часть 1; все части одного
	// запуска несут один JobRunID и исходный Type. Части лежат на
	// разных кассетах, поэтому Unique(tape_uuid, session_num) не
	// конфликтует. Нормализация Part < 1 → 1 делается при записи в
	// каталог (CreateSession), старые строки БД получают 1 миграцией.
	Part int32
}
