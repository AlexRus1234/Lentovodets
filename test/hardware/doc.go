// Package hardware содержит тесты на реальном LTO-стримере.
//
// Build tag "tape": запускаются только вручную на машине с приводом,
// командой `go test -tags=tape ./test/hardware/...`. В CI не выполняются.
//
// Два набора (Этапы 5 и 9): tape_test.go — сырые операции ленты
// (ярлык+EOF, навигация MTFSF/MTBSFM/MTEOM, eject); backup_test.go —
// сквозной сценарий format → backup → readtest → restore full на
// реальных use case'ах.
package hardware
