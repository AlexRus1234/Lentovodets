// Конструктор и тип FS.

package osfs

// New создаёт файловую систему. Ошибки нет: зависимостей и состояния
// у FS нет, все методы — тонкие обёртки над os.*.
func New() *FS { return &FS{} }

// FS реализует port.Walker + port.FileReader + port.FileWriter.
type FS struct{}
