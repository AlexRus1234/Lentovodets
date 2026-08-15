package tapeformat_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/testutil"
)

var update = flag.Bool("update", false, "перезаписать golden-файлы актуальными байтами")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

func TestGoldenLabel(t *testing.T) {
	block, err := tapeformat.EncodeLabel(fixtureLabel())
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	checkGolden(t, "label.bin", block)
}

func TestGoldenSession(t *testing.T) {
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs)

	blocks, marks := tape.Snapshot()
	checkGolden(t, "index.bin", blocks[0])

	var all []byte
	for _, b := range blocks {
		all = append(all, b...)
	}
	checkGolden(t, "session.bin", all)

	var sb strings.Builder
	for _, m := range marks {
		fmt.Fprintf(&sb, "%d\n", m)
	}
	checkGolden(t, "session_marks.txt", []byte(sb.String()))
}

func TestGoldenDeterministic(t *testing.T) {
	record := func() []byte {
		fs, idx := buildFixture(t)
		tape := testutil.NewFakeTape()
		writeFixtureSession(t, tape, idx, fs)
		blocks, _ := tape.Snapshot()
		var all []byte
		for _, b := range blocks {
			all = append(all, b...)
		}
		return all
	}
	if !bytes.Equal(record(), record()) {
		t.Error("две записи одной сессии дают разные байты")
	}
}

// checkGolden сравнивает байты с golden-файлом; при -update перезаписывает.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("golden", name)
	if *update {
		if err := os.MkdirAll("golden", 0o755); err != nil {
			t.Fatalf("MkdirAll golden: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("запись %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s (если формат менялся осознанно: go test ./internal/adapter/tapeformat -update): %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s: байты отличаются (want %d Б, got %d Б); если формат менялся осознанно: -update", path, len(want), len(got))
	}
}
