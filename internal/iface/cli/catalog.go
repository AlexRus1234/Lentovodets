// Команды группы catalog — клиент демона (SPEC §5: daemon-режим).

package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
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
				rt.printf("%4d %-4d %-24s %s %s\n", s.ID, s.Num, s.Type, s.TapeUUID,
					time.Unix(s.Timestamp, 0).UTC().Format(time.RFC3339))
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
