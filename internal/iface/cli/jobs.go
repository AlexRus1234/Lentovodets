// Команды группы jobs: list/add/remove (local, TOML).

package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"lentovodec/internal/domain"
)

// newJobsCmd — группа `lentovodec jobs ...`.
func newJobsCmd(deps Deps, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Управление заданиями бекапа в lentovodec.toml",
	}
	cmd.AddCommand(
		newJobsListCmd(deps, flags),
		newJobsAddCmd(deps, flags),
		newJobsRemoveCmd(deps, flags),
	)
	return cmd
}

// newJobsListCmd — `lentovodec jobs list`.
func newJobsListCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Показать задания из TOML",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			jobs, err := rt.cfg.Jobs()
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				rt.printf("заданий нет (lentovodec jobs add ...)\n")
				return nil
			}
			rt.printf("%-16s %-8s %s\n", "NAME", "MODE", "PATHS")
			for _, j := range jobs {
				rt.printf("%-16s %-8s %s\n", j.Name, j.Mode, strings.Join(j.Paths, ", "))
				if j.Description != "" {
					rt.printf("  %s\n", j.Description)
				}
			}
			return nil
		},
	}
}

// newJobsAddCmd — `lentovodec jobs add <name> --paths --mode --desc --exclude`.
func newJobsAddCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var pathsRaw, mode, desc, excludeRaw string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Добавить задание в TOML",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			job := domain.Job{
				Name:        args[0],
				Description: desc,
				Mode:        domain.JobMode(mode),
				Paths:       splitCSV(pathsRaw),
				Exclude:     splitCSV(excludeRaw),
			}
			if err := rt.cfg.AddJob(job); err != nil {
				return err
			}
			rt.printf("задание %s добавлено в %s\n", job.Name, rt.flags.config)
			return nil
		},
	}
	cmd.Flags().StringVar(&pathsRaw, "paths", "", "корни бекапа через запятую (обязательно)")
	cmd.Flags().StringVar(&mode, "mode", "append", "режим: append | mirror")
	cmd.Flags().StringVar(&desc, "desc", "", "описание")
	cmd.Flags().StringVar(&excludeRaw, "exclude", "", "glob-шаблоны исключений через запятую")
	return cmd
}

// newJobsRemoveCmd — `lentovodec jobs remove <name>`.
func newJobsRemoveCmd(deps Deps, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Удалить задание из TOML",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			if err := rt.cfg.RemoveJob(args[0]); err != nil {
				return err
			}
			rt.printf("задание %s удалено из %s\n", args[0], rt.flags.config)
			return nil
		},
	}
}

// splitCSV разбирает значения флага через запятую.
func splitCSV(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
