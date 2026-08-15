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
}
