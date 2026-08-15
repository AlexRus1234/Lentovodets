// Обход дерева каталогов поверх filepath.WalkDir.

package osfs

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

	"lentovodec/internal/port"
)

// Walk рекурсивно обходит дерево от root (включая сам root) и вызывает
// fn для каждого элемента. Пути — как их отдаёт filepath (разделители ОС).
// Ошибка из fn останавливает обход и возвращается наружу; то же —
// при отмене контекста.
func (f *FS) Walk(ctx context.Context, root string, fn func(path string, info port.Entry) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("osfs: обход %q: %w", root, err)
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("osfs: обход %q: %w", p, err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("osfs: обход %q: %w", root, err)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("osfs: stat %q: %w", p, err)
		}
		return fn(p, info)
	})
	if err != nil {
		return fmt.Errorf("osfs: обход %q: %w", root, err)
	}
	return nil
}
