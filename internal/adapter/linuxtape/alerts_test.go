//go:build tape && linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || s390x)

package linuxtape

import (
	"encoding/binary"
	"strings"
	"testing"
)

func tapeAlertResponse(params ...[]byte) []byte {
	length := 0
	for _, p := range params {
		length += len(p)
	}
	data := make([]byte, 4+length)
	data[0] = 0x2e
	binary.BigEndian.PutUint16(data[2:4], uint16(length))
	pos := 4
	for _, p := range params {
		copy(data[pos:], p)
		pos += len(p)
	}
	return data
}

func tapeAlertParam(code int, value ...byte) []byte {
	param := make([]byte, 4+len(value))
	binary.BigEndian.PutUint16(param[:2], uint16(0x7ff+code))
	param[3] = byte(len(value))
	copy(param[4:], value)
	return param
}

func TestParseTapeAlertPage_ActiveAndUnknown(t *testing.T) {
	data := tapeAlertResponse(
		tapeAlertParam(1, 0x01),
		tapeAlertParam(2, 0x01),
		tapeAlertParam(42, 0x01),
		tapeAlertParam(3, 0x02),
	)
	got, err := parseTapeAlertPage(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 3 || got[0].Code != 1 || got[0].Name != "read-failure" || !got[1].Critical || got[2].Name != "alert-0x2A" {
		t.Fatalf("alerts = %+v", got)
	}
}

func TestParseTapeAlertPage_Empty(t *testing.T) {
	got, err := parseTapeAlertPage([]byte{0x2e, 0, 0, 0})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty page = %+v, %v", got, err)
	}
}

func TestParseTapeAlertPage_RejectsWrongPage(t *testing.T) {
	data := []byte{0x2d, 0, 0, 0}
	if _, err := parseTapeAlertPage(data); err == nil {
		t.Fatal("wrong page: expected error")
	}
}

func TestParseTapeAlertPage_UsesFlagOneBit(t *testing.T) {
	data := tapeAlertResponse(tapeAlertParam(1, 0x02), tapeAlertParam(20, 0x01))
	got, err := parseTapeAlertPage(data)
	if err != nil || len(got) != 1 || got[0].Code != 20 || got[0].Name != "clean-now" {
		t.Fatalf("alerts = %+v, err = %v", got, err)
	}
}

func TestParseTapeAlertPage_Truncated(t *testing.T) {
	cases := [][]byte{
		{0x2e, 0, 0},
		{0x2e, 0, 0, 4, 0x08, 0x01, 0, 1},
		tapeAlertResponse([]byte{0x08, 0x01, 0, 3, 1}),
	}
	for _, data := range cases {
		if _, err := parseTapeAlertPage(data); err == nil || !strings.Contains(err.Error(), "TapeAlert") {
			t.Errorf("parse %x: err = %v", data, err)
		}
	}
}

func TestTapeAlertName_Unknown(t *testing.T) {
	name, critical := tapeAlertName(0x99)
	if name != "alert-0x99" || critical {
		t.Fatalf("unknown = %q, %v", name, critical)
	}
}

func TestTapeAlertName_PowerConsumptionAndMaintenance(t *testing.T) {
	name, critical := tapeAlertName(28)
	if name != "power-consumption" || critical {
		t.Fatalf("flag 28 = %q, %v", name, critical)
	}
	name, critical = tapeAlertName(29)
	if name != "drive-maintenance" || critical {
		t.Fatalf("flag 29 = %q, %v", name, critical)
	}
}
