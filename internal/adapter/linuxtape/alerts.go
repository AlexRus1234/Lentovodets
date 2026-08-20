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
	"runtime"
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
	runtime.KeepAlive(cdb)
	runtime.KeepAlive(sense)
	runtime.KeepAlive(response)
	return parseTapeAlertPage(response)
}

func sendSGIO(fd uintptr, hdr *sgIOHeader) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, uintptr(sgIO), uintptr(unsafe.Pointer(hdr)))
	if errno != 0 {
		return fmt.Errorf("linuxtape: SG_IO LOG SENSE: %w", errno)
	}
	if hdr.status != 0 || hdr.maskedStatus != 0 || hdr.hostStatus != 0 || hdr.driverStatus != 0 {
		return fmt.Errorf("linuxtape: SG_IO status=%d masked=%d host=%d driver=%d", hdr.status, hdr.maskedStatus, hdr.hostStatus, hdr.driverStatus)
	}
	return nil
}

func parseTapeAlertPage(data []byte) ([]domain.TapeAlert, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("linuxtape: TapeAlert response too short: %d", len(data))
	}
	if data[0]&0x3f != logSensePage {
		return nil, fmt.Errorf("linuxtape: unexpected TapeAlert page 0x%02x", data[0]&0x3f)
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
		code := int(binary.BigEndian.Uint16(data[pos:pos+2])) - 0x7ff
		valueLen := int(data[pos+3])
		pos += 4
		if valueLen > length-pos {
			return nil, fmt.Errorf("linuxtape: truncated TapeAlert parameter %d", code)
		}
		active := valueLen > 0 && data[pos]&1 != 0
		if active && code > 0 {
			name, critical := tapeAlertName(code)
			alerts = append(alerts, domain.TapeAlert{Name: name, Code: code, Critical: critical})
		}
		pos += valueLen
	}
	return alerts, nil
}

func tapeAlertName(code int) (string, bool) {
	switch code {
	case 1:
		return "read-failure", true
	case 2:
		return "write-failure", true
	case 3:
		return "hard-error", true
	case 4:
		return "media", true
	case 5:
		return "write-protect-error", false
	case 7:
		return "media-life", false
	case 8:
		return "not-data-grade", true
	case 9:
		return "no-removal", false
	case 10:
		return "cleaning-media", false
	case 12:
		return "worm-cartridge", true
	case 20:
		return "clean-now", true
	case 21:
		return "cleaning-still-in-progress", false
	case 22:
		return "expired-cleaning-media", true
	case 23:
		return "invalid-cleaning-media", true
	case 24:
		return "retension-requested", false
	case 25:
		return "dual-port-interface-error", true
	case 26:
		return "cooling-fan-failure", true
	case 27:
		return "power-supply-failure", true
	case 28:
		return "drive-maintenance", false
	}
	return fmt.Sprintf("alert-0x%02X", code), false
}
