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

// Команда backup: запуск задания бекапа (local-режим, без демона).

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"lentovodec/internal/port"
	"lentovodec/internal/usecase/backup"
)

// newBackupCmd — `lentovodec backup <job> [--full] [--dry-run]
// [--verify] [--next-tape NAME]`.
func newBackupCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var full, dryRun, verify bool
	var nextTape string
	cmd := &cobra.Command{
		Use:   "backup <job>",
		Short: "Запустить задание бекапа (прямой доступ к ленте)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			return rt.runLocalTape(func(ctx context.Context, tape port.Tape, cat port.Catalog) error {
				changer := &stdinChanger{
					deps: deps, cfg: rt.cfg, cat: cat, codec: deps.Codec,
					rand: deps.Rand, clock: deps.Clock, log: rt.logger(),
					job: args[0], nextName: nextTape,
				}
				uc := backup.New(rt.cfg, tape, deps.Codec, cat, deps.FS,
					deps.Hasher, deps.Rand, deps.Clock, nil, rt.logger(), changer)
				res, err := uc.Backup(ctx, args[0], backup.Options{Full: full, DryRun: dryRun, Verify: verify})
				if err != nil {
					return err
				}
				if dryRun {
					rt.printf("dry-run: %s\n", statsLine(res.Stats))
					return nil
				}
				rt.printf("сессия #%d %s (id %d) на кассете %s\n",
					res.Session.Num, res.Session.Type, res.Session.ID, res.Session.TapeUUID)
				if res.Parts > 1 {
					rt.printf("частей %d на кассетах: %s\n", res.Parts, strings.Join(res.Tapes, ", "))
				}
				if res.Verified {
					rt.printf("верифицировано: %d файлов (%s)\n",
						res.VerifiedFiles, humanSize(res.VerifiedBytes))
				}
				rt.printf("%s\n", statsLine(res.Stats))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "полный бекап (перезапись ленты с сессии 1)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "только сканирование, без записи")
	cmd.Flags().BoolVar(&verify, "verify", false,
		"прочитать записанное обратно и сверить хеши сразу после записи (примерно ×2 к времени)")
	cmd.Flags().StringVar(&nextTape, "next-tape", "",
		"имя следующей кассеты для неинтерактивного spanning (--next-tape media-014)")
	return cmd
}

// statsLine — компактная строка статистики.
func statsLine(st backup.Stats) string {
	return fmt.Sprintf("изменено %d: добавлено %d, изменено %d, удалено %d; к записи %s",
		st.Scanned, st.Added, st.Modified, st.Deleted, humanSize(st.Bytes))
}
