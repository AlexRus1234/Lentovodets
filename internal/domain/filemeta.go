// Метаданные файла в сессии. См. docs/SPECIFICATION.md §2.4–2.5
// и docs/FORMAT.md §6.

package domain

// FileState — состояние файла относительно предыдущей сессии.
// Значение совпадает с символом в БД и JSON-индексе на ленте.
type FileState string

// Допустимые значения FileState.
const (
	// StateAdded — файл есть на ФС, в прошлой сессии не встречался ('A').
	StateAdded FileState = "A"

	// StateModified — файл есть, изменился Size или ModTime ('M').
	StateModified FileState = "M"

	// StateDeleted — tombstone: файл был в прошлой сессии, теперь
	// отсутствует; только режим mirror ('D'). В tar-поток не попадает.
	StateDeleted FileState = "D"
)

// Valid сообщает, что значение — один из допустимых символов состояния.
func (s FileState) Valid() bool {
	return s == StateAdded || s == StateModified || s == StateDeleted
}

// FileMeta — метаданные одного файла в конкретной сессии.
type FileMeta struct {
	Path    string    `json:"path"`     // всегда NormalizePath
	Size    int64     `json:"size"`     // байты; 0 для каталогов
	ModTime int64     `json:"mod_time"` // Unix-наносекунды mtime файла
	IsDir   bool      `json:"is_dir"`   // true для каталогов
	Hash    string    `json:"hash"`     // xxhash64 hex (16 символов); "" для каталогов и tombstone'ов
	State   FileState `json:"state"`
}

// IsAdded сообщает, что файл новый в этой сессии.
func (fm FileMeta) IsAdded() bool { return fm.State == StateAdded }

// IsDeleted сообщает, что запись — tombstone на удалённый файл.
func (fm FileMeta) IsDeleted() bool { return fm.State == StateDeleted }
