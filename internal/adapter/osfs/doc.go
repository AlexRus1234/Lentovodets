// Package osfs реализует port.Filesystem поверх os.*.
//
// Walker / FileReader / FileWriter / Stater — тонкие обёртки над os.Stat,
// os.Open, os.Create, os.MkdirAll, filepath.WalkDir. Используется в
// реальном бекапе/восстановлении; в тестах заменяется на testutil.MapFS.
package osfs
