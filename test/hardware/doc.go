// Package hardware содержит тесты на реальном LTO-стримере.
//
// Build tag "tape": запускаются только вручную на машине с приводом,
// командой `go test -tags=tape ./test/hardware/...`. В CI не выполняются.
//
// Реализуется после Этапа 5 (adapter/linuxtape) и Этапа 9.
package hardware
