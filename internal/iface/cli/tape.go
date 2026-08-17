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

// Команды группы tape: format/readtest (local), info/eject (daemon).

package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/catalog"
	"lentovodec/internal/usecase/format"
)

// newTapeCmd — группа `lentovodec tape ...`.
func newTapeCmd(deps Deps, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tape",
		Short: "Управление лентой",
	}
	cmd.AddCommand(
		newTapeFormatCmd(deps, flags),
		newTapeReadtestCmd(deps, flags),
		newTapeInfoCmd(deps, flags),
		newTapeEjectCmd(deps, flags),
	)
	return cmd
}

// newTapeFormatCmd — `lentovodec tape format <name> [--force]`.
func newTapeFormatCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "format <name>",
		Short: "Отформатировать ленту (ярлык + двойной EOF + каталог)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			return rt.runLocal(func(ctx context.Context, tape port.Tape, cat port.Catalog) error {
				uc := format.New(tape, deps.Codec, cat, deps.Rand, deps.Clock, rt.logger())
				label, err := uc.Format(ctx, args[0], force)
				if err != nil {
					var already *domain.AlreadyFormattedError
					if errors.As(err, &already) {
						return fmt.Errorf("%w (используйте --force)", err)
					}
					return err
				}
				rt.printf("кассета отформатирована: %s (uuid %s)\n", label.Name, label.UUID)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "форматировать даже отформатированную ленту")
	return cmd
}

// newTapeReadtestCmd — `lentovodec tape readtest`.
func newTapeReadtestCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "readtest",
		Short: "Диагностическое чтение всей ленты со сверкой хешей",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			return rt.runLocalTape(func(ctx context.Context, tape port.Tape, cat port.Catalog) error {
				uc := catalog.New(cat, tape, deps.Codec, nil, rt.logger(),
					restoreChangerFor(deps, rt))
				reports, err := uc.ReadTest(ctx)
				if err != nil {
					return err
				}
				total := 0
				for _, rep := range reports {
					rt.printf("кассета %s: сессий %d, файлов %d (%s)\n",
						rep.Name, rep.Sessions, rep.Files, humanSize(rep.Bytes))
					total += rep.Sessions
				}
				rt.printf("проверено сессий: %d\n", total)
				return nil
			})
		},
	}
}

// newTapeInfoCmd — `lentovodec tape info` (через демона).
func newTapeInfoCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Прочитать ярлык ленты (через демона)",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			info, err := rt.dial().TapeInfo(context.Background())
			if err != nil {
				return err
			}
			rt.printf("name        %s\n", info.Label.Name)
			rt.printf("uuid        %s\n", info.Label.UUID)
			rt.printf("magic       %s\n", info.Label.Magic)
			rt.printf("version     %d\n", info.Label.FormatVersion)
			rt.printf("formatted   %s\n", info.Label.FormattedAt)
			return nil
		},
	}
}

// newTapeEjectCmd — `lentovodec tape eject` (через демона).
func newTapeEjectCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "eject",
		Short: "Извлечь ленту (через демона)",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			if err := rt.dial().Eject(context.Background()); err != nil {
				return err
			}
			rt.printf("лента извлечена\n")
			return nil
		},
	}
}
