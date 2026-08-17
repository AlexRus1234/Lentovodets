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

// Корневая cobra-команда, глобальные флаги и общий рантайм команд.
// См. docs/SPECIFICATION.md §5.

package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"lentovodec/internal/adapter/sloglog"
	"lentovodec/internal/port"
)

// globalFlags — глобальные флаги, наследуемые подкомандами (SPEC §5).
type globalFlags struct {
	config  string
	device  string
	db      string
	log     string
	server  string
	verbose bool
}

// Execute разбирает args и выполняет команду; все зависимости — в deps.
func Execute(args []string, deps Deps) error {
	root := newRootCmd(deps)
	root.SetArgs(args)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	return root.Execute()
}

// newRootCmd собирает дерево команд.
func newRootCmd(deps Deps) *cobra.Command {
	flags := &globalFlags{}
	root := &cobra.Command{
		Use:     "lentovodec",
		Short:   "Резервное копирование на ленточные стримеры LTO",
		Version: deps.Version,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&flags.config, "config", "lentovodec.toml", "путь к конфигу")
	pf.StringVar(&flags.device, "device", "", "устройство ленты (по умолчанию /dev/nst0)")
	pf.StringVar(&flags.db, "db", "", "путь к SQLite-каталогу")
	pf.StringVar(&flags.log, "log", "", "файл лога")
	pf.StringVar(&flags.server, "server", "", "адрес демона для клиентских команд")
	pf.BoolVarP(&flags.verbose, "verbose", "v", false, "отладочный лог")

	root.AddCommand(
		newBackupCmd(deps, flags),
		newRestoreCmd(deps, flags),
		newTapeCmd(deps, flags),
		newJobsCmd(deps, flags),
		newCatalogCmd(deps, flags),
		newPasswdCmd(deps),
		newDaemonCmd(deps, flags),
	)
	return root
}

// runtime — открытые по требованию общие ресурсы команды.
type runtime struct {
	deps  Deps
	flags *globalFlags
	cfg   ConfigFile
}

// openRuntime открывает конфигурацию с учётом глобальных флагов.
func openRuntime(deps Deps, flags *globalFlags) (*runtime, error) {
	overrides := map[string]string{}
	if flags.device != "" {
		overrides["device"] = flags.device
	}
	if flags.db != "" {
		overrides["db"] = flags.db
	}
	if flags.log != "" {
		overrides["log"] = flags.log
	}
	if flags.server != "" {
		overrides["server"] = flags.server
	}
	if flags.verbose {
		overrides["log_level"] = "debug"
	}
	cfg, err := deps.OpenConfig(flags.config, overrides)
	if err != nil {
		return nil, err
	}
	return &runtime{deps: deps, flags: flags, cfg: cfg}, nil
}

// runLocal открывает ленту и каталог, выполняет fn и закрывает всё.
// Probe доступа к устройству (rootless-модель, SPEC §9.1) делает
// OpenTape из wire: EACCES/ENOENT → понятная ошибка с подсказкой.
func (rt *runtime) runLocal(fn func(ctx context.Context, tape port.Tape, cat port.Catalog) error) error {
	tape, err := rt.deps.OpenTape(rt.cfg.Device())
	if err != nil {
		return err
	}
	defer func() {
		if err := tape.Close(); err != nil {
			fmt.Fprintf(rt.deps.Stderr, "закрытие ленты: %v\n", err)
		}
	}()
	cat, err := rt.deps.OpenCatalog(rt.cfg.DB())
	if err != nil {
		return err
	}
	defer func() {
		if err := cat.Close(); err != nil {
			fmt.Fprintf(rt.deps.Stderr, "закрытие каталога: %v\n", err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return fn(ctx, tape, cat)
}

// runLocalTape — runLocal для команд, меняющих ленту в процессе
// (spanning-бекап):changer закрывает исходную ленту сам, повторное
// закрытие подавляется обёрткой closeOnce.
func (rt *runtime) runLocalTape(fn func(ctx context.Context, tape port.Tape, cat port.Catalog) error) error {
	return rt.runLocal(func(ctx context.Context, tape port.Tape, cat port.Catalog) error {
		return fn(ctx, &closeOnce{Tape: tape}, cat)
	})
}

// closeOnce — port.Tape, закрываемый не более одного раза: повторное
// закрытие возвращает результат первого.
type closeOnce struct {
	port.Tape
	once sync.Once
	err  error
}

// Close закрывает ленту один раз.
func (t *closeOnce) Close() error {
	t.once.Do(func() { t.err = t.Tape.Close() })
	return t.err
}

// dial — клиент демона для daemon-команд.
func (rt *runtime) dial() ServerClient {
	return rt.deps.DialServer(rt.cfg.Server(), rt.cfg.APIKey(), rt.cfg.WebUsername(), rt.deps.AskPassword)
}

// logger — логгер команды (stderr, уровень из конфига).
func (rt *runtime) logger() *slog.Logger {
	return sloglog.New(rt.cfg.LogLevel(), rt.deps.Stderr)
}

// printf — вывод в stdout команды.
func (rt *runtime) printf(format string, args ...any) {
	fmt.Fprintf(rt.deps.Stdout, format, args...)
}

// humanSize — компактный размер в байтах для вывода.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
