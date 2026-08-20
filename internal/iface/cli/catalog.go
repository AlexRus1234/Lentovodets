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

// Команды группы catalog: rebuild — local-режим (прямой доступ к ленте
// и каталогу, как tape readtest); остальные — клиенты демона
// (SPEC §5: daemon-режим).

package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"lentovodec/internal/port"
	"lentovodec/internal/usecase/catalog"
)

// newCatalogCmd — группа `lentovodec catalog ...`.
func newCatalogCmd(deps Deps, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Запросы к каталогу демона",
	}
	cmd.AddCommand(
		newCatalogTapesCmd(deps, flags),
		newCatalogSessionsCmd(deps, flags),
		newCatalogFilesCmd(deps, flags),
		newCatalogSearchCmd(deps, flags),
		newCatalogRmCmd(deps, flags),
		newCatalogPruneCmd(deps, flags),
		newCatalogRebuildCmd(deps, flags),
	)
	return cmd
}

// newCatalogTapesCmd — `catalog tapes`.
func newCatalogTapesCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "tapes",
		Short: "Список кассет",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			tapes, err := rt.dial().ListTapes(context.Background())
			if err != nil {
				return err
			}
			rt.printf("%-24s %-20s %s\n", "UUID", "NAME", "FORMATTED")
			for _, t := range tapes {
				rt.printf("%-24s %-20s %s\n",
					t.UUID, t.Name, time.Unix(t.FormattedAt, 0).UTC().Format(time.RFC3339))
			}
			return nil
		},
	}
}

// newCatalogSessionsCmd — `catalog sessions [--tape U]`.
func newCatalogSessionsCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var tape string
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Список сессий",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			sessions, err := rt.dial().ListSessions(context.Background(), tape)
			if err != nil {
				return err
			}
			rt.printf("%4s %-4s %-24s %s\n", "ID", "NUM", "TYPE", "TAPE/время")
			for _, s := range sessions {
				line := fmt.Sprintf("%4d %-4d %-24s %s %s", s.ID, s.Num, s.Type, s.TapeUUID,
					time.Unix(s.Timestamp, 0).UTC().Format(time.RFC3339))
				if s.Part > 1 {
					line += fmt.Sprintf(" part %d", s.Part)
				}
				rt.printf("%s\n", line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tape, "tape", "", "фильтр по uuid кассеты")
	return cmd
}

// newCatalogFilesCmd — `catalog files --session N`.
func newCatalogFilesCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var session int64
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Файлы сессии",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			files, err := rt.dial().SessionFiles(context.Background(), session)
			if err != nil {
				return err
			}
			rt.printf("%-1s %10s %s\n", "S", "SIZE", "PATH")
			for _, f := range files {
				rt.printf("%-1s %10s %s\n", string(f.State), strconv.FormatInt(f.Size, 10), f.Path)
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&session, "session", 0, "id сессии (обязательно)")
	_ = cmd.MarkFlagRequired("session")
	return cmd
}

// newCatalogSearchCmd — `catalog search <pattern>`.
func newCatalogSearchCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "search <pattern>",
		Short: "Поиск файлов по подстроке пути",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			copies, err := rt.dial().Search(context.Background(), args[0])
			if err != nil {
				return err
			}
			for _, cp := range copies {
				rt.printf("сессия %d (#%d, %s): %s\n",
					cp.SessionID, cp.SessionNum, cp.TapeUUID, cp.Meta.Path)
			}
			if len(copies) == 0 {
				rt.printf("ничего не найдено\n")
			}
			return nil
		},
	}
}

// newCatalogRmCmd — `catalog rm --session N`.
func newCatalogRmCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var session int64
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Удалить сессию из каталога (данные на ленте остаются)",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			if err := rt.dial().DeleteSession(context.Background(), session); err != nil {
				return err
			}
			rt.printf("сессия %d удалена из каталога\n", session)
			return nil
		},
	}
	cmd.Flags().Int64Var(&session, "session", 0, "id сессии (обязательно)")
	_ = cmd.MarkFlagRequired("session")
	return cmd
}

// newCatalogPruneCmd — `catalog prune --days N`.
func newCatalogPruneCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var days int64
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Удалить сессии старше N дней",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			if days <= 0 {
				return fmt.Errorf("--days должно быть положительным числом")
			}
			deleted, err := rt.dial().Prune(context.Background(), days)
			if err != nil {
				return err
			}
			rt.printf("удалено сессий: %d\n", deleted)
			return nil
		},
	}
	cmd.Flags().Int64Var(&days, "days", 0, "возраст сессий в днях (обязательно)")
	_ = cmd.MarkFlagRequired("days")
	return cmd
}

// newCatalogRebuildCmd — `catalog rebuild`: пересобрать каталог из
// индексов вставленной кассеты (local-режим: лента + каталог напрямую,
// демон не нужен и не должен параллельно занимать стример).
func newCatalogRebuildCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Пересобрать каталог из индексов вставленной кассеты (DR; локально, без демона)",
		Long: `Пересобирает каталог из содержимого вставленной кассеты: ярлык и
JSON-индексы сессий переносятся в базу, tar-поток не читается
(быстро; целостность данных проверяет tape readtest). Сценарии:
утерян или повреждён lentovodec.db, кассета, неизвестная каталогу,
переезд на новую машину. Повторный запуск безопасен: существующие
сессии пропускаются.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			return rt.runLocal(func(ctx context.Context, tape port.Tape, cat port.Catalog) error {
				uc := catalog.New(cat, tape, deps.Codec, nil, rt.logger(), nil)
				rep, err := uc.Rebuild(ctx)
				if err != nil {
					return err
				}
				rt.printf("кассета: %s\n", rep.TapeName)
				rt.printf("добавлено сессий: %d\n", rep.Sessions)
				rt.printf("пропущено сессий (уже в каталоге): %d\n", rep.SkippedSessions)
				rt.printf("файлов записано: %d\n", rep.Files)
				if rep.NextTapeName != "" {
					rt.printf("цепочка продолжается: вставьте кассету %s и повторите rebuild\n",
						rep.NextTapeName)
				}
				return nil
			})
		},
	}
}
