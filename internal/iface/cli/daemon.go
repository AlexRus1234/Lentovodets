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

// Команда daemon: HTTP-демон с REST API и фоновыми задачами.

package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
)

// newDaemonCmd — `lentovodec daemon [--port] [--bind]`.
func newDaemonCmd(deps Deps, flags *globalFlags) *cobra.Command {
	var bind string
	var port int
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Запустить HTTP-демона (REST API + Web UI)",
		RunE: func(_ *cobra.Command, _ []string) error {
			rt, err := openRuntime(deps, flags)
			if err != nil {
				return err
			}
			cat, err := deps.OpenCatalog(rt.cfg.DB())
			if err != nil {
				return err
			}
			override := ""
			if bind != "" || port != 0 {
				override = joinBind(bind, port, rt.cfg.Bind())
			}
			daemon, err := deps.NewDaemon(DaemonOpts{
				Version:      deps.Version,
				Config:       rt.cfg,
				Logger:       rt.logger(),
				BindOverride: override,
				Catalog:      cat,
				OpenTape:     deps.OpenTape,
				FS:           deps.FS,
				Codec:        deps.Codec,
				Hasher:       deps.Hasher,
				Rand:         deps.Rand,
				Clock:        deps.Clock,
			})
			if err != nil {
				return errors.Join(err, cat.Close())
			}
			fmt.Fprintf(deps.Stderr, "демон слушает %s\n", daemon.BindAddr())

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return daemon.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&bind, "bind", "", "адрес для слушания (по умолчанию из конфига)")
	cmd.Flags().IntVar(&port, "port", 0, "порт (по умолчанию из конфига)")
	return cmd
}

// joinBind собирает адрес из флагов поверх адреса конфига: флаги
// бьют соответствующую часть (host и/или port) по отдельности.
func joinBind(bindFlag string, portFlag int, configBind string) string {
	host, portStr, err := net.SplitHostPort(configBind)
	if err != nil {
		host, portStr = "127.0.0.1", "29201"
	}
	if bindFlag != "" {
		host = bindFlag
	}
	if portFlag != 0 {
		portStr = strconv.Itoa(portFlag)
	}
	if host == "" {
		return ":" + portStr
	}
	return net.JoinHostPort(host, portStr)
}
