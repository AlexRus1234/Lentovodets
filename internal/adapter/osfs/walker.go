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
