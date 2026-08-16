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

// Команда restore: восстановление с ленты (local-режим). --paths →
// smart-восстановление, иначе — полное восстановление ленты.

package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"lentovodec/internal/iface/destfs"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/restore"
)

// newRestoreCmd — `lentovodec restore [--paths p1,p2] [--dest P] [--original]`.
func newRestoreCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var pathsRaw, dest string
	var original bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Восстановить файлы с ленты (прямой доступ к ленте)",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			var paths []string
			for _, p := range strings.Split(pathsRaw, ",") {
				if p = strings.TrimSpace(p); p != "" {
					paths = append(paths, p)
				}
			}
			if original {
				dest = ""
			}
			return rt.runLocal(func(ctx context.Context, tape port.Tape, cat port.Catalog) error {
				fs := deps.FS
				if dest != "" {
					fs = destfs.Wrap(deps.FS, dest)
				}
				uc := restore.New(tape, deps.Codec, cat, fs, nil, rt.logger())
				var st restore.Stats
				if len(paths) > 0 {
					st, err = uc.Smart(ctx, paths)
				} else {
					st, err = uc.Full(ctx)
				}
				if err != nil {
					return err
				}
				rt.printf("восстановлено файлов %d, каталогов %d, пропущено %d (сессий прочитано %d)\n",
					st.Files, st.Dirs, st.Skipped, st.Sessions)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&pathsRaw, "paths", "", "пути через запятую (smart-восстановление); пусто — вся лента")
	cmd.Flags().StringVar(&dest, "dest", "", "восстановить в указанный каталог, а не по исходным путям")
	cmd.Flags().BoolVar(&original, "original", false, "восстановить по исходным путям из индекса (игнорирует --dest)")
	return cmd
}
