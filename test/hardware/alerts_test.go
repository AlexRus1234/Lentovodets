//go:build tape && linux

package hardware

import (
	"context"
	"os"
	"testing"

	"lentovodec/internal/adapter/linuxtape"
)

// TestTapeAlerts печатает активные TapeAlert-флаги реального привода.
// Кассета не нужна (лог-страницы привода), но нужен CAP_SYS_RAWIO:
// LOG SENSE не в kernel-whitelist SG_IO для непривилегированных
// (EPERM под обычным пользователем; живой прогон SKLD00 —
// логи/стенд-arch-skld00.md). Запуск:
//
//	sudo env LENTOVODEC_TAPE_DIAG=1 LENTOVODEC_TAPE_DEVICE=/dev/nst0 \
//	  "$(command -v go)" test -tags=tape ./test/hardware -run TestTapeAlerts -v
func TestTapeAlerts(t *testing.T) {
	if os.Getenv("LENTOVODEC_TAPE_DIAG") == "" {
		t.Skip("диагностика выключена: LENTOVODEC_TAPE_DIAG=1")
	}
	tape, err := linuxtape.Open(devicePath(t))
	if err != nil {
		t.Fatalf("открытие устройства: %v", err)
	}
	defer func() { _ = tape.Close() }()
	alerts, err := tape.TapeAlerts(context.Background())
	if err != nil {
		t.Fatalf("TapeAlert: %v", err)
	}
	for _, alert := range alerts {
		t.Logf("TapeAlert code=%d critical=%t name=%s", alert.Code, alert.Critical, alert.Name)
	}
}
