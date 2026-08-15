package tapeformat

import (
	"strings"
	"testing"

	"lentovodec/internal/port"
)

// encodeBlocks обязан возвращать ошибку для значений, которые не умеет
// кодировать encoding/json (наши wire-типы к таким не относятся, но ветка
// обязана работать).
func TestEncodeBlocks_UnmarshalableValue(t *testing.T) {
	_, err := encodeBlocks(struct{ Ch chan int }{}, 0)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "tapeformat: JSON") {
		t.Errorf("err = %v, want вхождение %q", err, "tapeformat: JSON")
	}
}

// discardProgress — заглушка для nil-прогресса; методы не должны паниковать.
func TestDiscardProgress(t *testing.T) {
	var prog port.ProgressReporter = discardProgress{}
	prog.Update(port.ProgressUpdate{Phase: port.PhaseWrite, CurrentFile: "/x"})
	prog.Done()
	prog.Fail(nil)
}
