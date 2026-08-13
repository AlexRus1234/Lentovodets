// Package main собирает бинарь lentovodec.
//
// Здесь только DI-композиция (wiring) адаптеров и use case'ов; вся
// бизнес-логика живёт в internal/usecase, доставка — в internal/iface.
// На Этапе 0 это скелет: реальный wiring появится в Этапе 7.
package main

// Version подставляется линкером через -ldflags "-X main.Version=...".
// Единственная разрешённая package-level переменная (см. ARCHITECTURE §6.2).
var Version = "dev"

func main() {
	_ = Version
}
