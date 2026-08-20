// Лентоводец — система резервного копирования на ленточные накопители LTO
// Copyright (C) 2026 AlexRus1234
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build tape && linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || s390x)

package linuxtape

import (
	"context"
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"

	"lentovodec/internal/domain"
)

const (
	sgIO           = 0x2285
	sgDxferFromDev = -3
	logSensePage   = 0x2e
)

// sgIOHeader is struct sg_io_hdr from linux/sg.h (SCSI generic v3).
type sgIOHeader struct {
	interfaceID    int32
	dataDirection  int32
	cmdLength      uint8
	maxSenseLength uint8
	iovecCount     uint16
	transferLength uint32
	transferPtr    uintptr
	cmdPtr         uintptr
	sensePtr       uintptr
	timeout        uint32
	flags          uint32
	packID         int32
	usrPtr         uintptr
	status         uint8
	maskedStatus   uint8
	msgStatus      uint8
	byteCount      uint8
	hostStatus     uint16
	driverStatus   uint16
	resid          int32
	duration       uint32
	info2          uint32
}

// TapeAlerts reads the current TapeAlert flags using LOG SENSE page 0x2e.
func (t *Tape) TapeAlerts(ctx context.Context) ([]domain.TapeAlert, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response := make([]byte, 4096)
	cdb := [10]byte{0x4d, 0x40, logSensePage, 0, 0, 0, 0, byte(len(response) >> 8), byte(len(response)), 0}
	var sense [64]byte
	hdr := sgIOHeader{
		interfaceID:    int32('S'),
		dataDirection:  sgDxferFromDev,
		cmdLength:      uint8(len(cdb)),
		maxSenseLength: uint8(len(sense)),
		timeout:        30000,
		transferLength: uint32(len(response)),
		transferPtr:    uintptr(unsafe.Pointer(&response[0])),
		cmdPtr:         uintptr(unsafe.Pointer(&cdb[0])),
		sensePtr:       uintptr(unsafe.Pointer(&sense[0])),
	}
	if err := sendSGIO(t.f.Fd(), &hdr); err != nil {
		return nil, err
	}
	return parseTapeAlertPage(response)
}

func sendSGIO(fd uintptr, hdr *sgIOHeader) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, uintptr(sgIO), uintptr(unsafe.Pointer(hdr)))
	if errno != 0 {
		return fmt.Errorf("linuxtape: SG_IO LOG SENSE: %w", errno)
	}
	if hdr.status != 0 || hdr.maskedStatus != 0 || hdr.hostStatus != 0 {
		return fmt.Errorf("linuxtape: SG_IO status=%d masked=%d host=%d", hdr.status, hdr.maskedStatus, hdr.hostStatus)
	}
	return nil
}

func parseTapeAlertPage(data []byte) ([]domain.TapeAlert, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("linuxtape: TapeAlert response too short: %d", len(data))
	}
	length := int(binary.BigEndian.Uint16(data[2:4])) + 4
	if length > len(data) {
		return nil, fmt.Errorf("linuxtape: TapeAlert response truncated: need %d, got %d", length, len(data))
	}
	var alerts []domain.TapeAlert
	for pos := 4; pos < length; {
		if length-pos < 4 {
			return nil, fmt.Errorf("linuxtape: truncated TapeAlert parameter header")
		}
		code := int(binary.BigEndian.Uint16(data[pos:pos+2])) - 0x800
		valueLen := int(data[pos+3])
		pos += 4
		if valueLen > length-pos {
			return nil, fmt.Errorf("linuxtape: truncated TapeAlert parameter %d", code)
		}
		active := false
		for _, b := range data[pos : pos+valueLen] {
			if b != 0 {
				active = true
				break
			}
		}
		if active && code > 0 {
			name, critical := tapeAlertName(code)
			alerts = append(alerts, domain.TapeAlert{Name: name, Code: code, Critical: critical})
		}
		pos += valueLen
	}
	return alerts, nil
}

func tapeAlertName(code int) (string, bool) {
	known := map[int]struct {
		name     string
		critical bool
	}{
		1: {"read-warning", false}, 2: {"read-failure", true},
		3: {"write-warning", false}, 4: {"write-failure", true},
		5: {"media-life", false}, 11: {"cleaning-media", false},
		12: {"unsupported-format", true}, 20: {"clean-now", true},
		21: {"clean-periodic", false}, 22: {"expired-cleaning-media", true},
		23: {"invalid-cleaning-media", true}, 24: {"retension-requested", false},
		25: {"dual-port-interface-error", true}, 26: {"cooling-fan-failure", true},
		27: {"power-supply-failure", true}, 28: {"drive-maintenance", false},
	}
	if item, ok := known[code]; ok {
		return item.name, item.critical
	}
	return fmt.Sprintf("alert-0x%02X", code), false
}
