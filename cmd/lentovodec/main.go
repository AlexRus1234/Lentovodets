// Package main собирает бинарь lentovodec.
//
// Здесь только DI-композиция (wiring) адаптеров и use case'ов; вся
// бизнес-логика живёт в internal/usecase, доставка — в internal/iface.
// Реальный wiring — в internal/iface/cli/wire.go (Этап 7).
package main

import (
	"fmt"
	"os"

	"lentovodec/internal/iface/cli"
)

// Version подставляется линкером через -ldflags "-X main.Version=...".
// Единственная разрешенная package-level переменная (см. ARCHITECTURE §6.2).
var Version = "dev"

func main() {
	if err := cli.Execute(os.Args[1:], cli.DefaultDeps(Version)); err != nil {
		fmt.Fprintln(os.Stderr, "lentovodec:", err)
		os.Exit(1)
	}
}
