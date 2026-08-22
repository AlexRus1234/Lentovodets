// Лентоводец — система резервного копирования на ленточные накопители LTO
// Copyright (C) 2026 AlexRus1234
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Внутренний тест linkID: фолбэки отражения для платформ без
// экспонирования inode (ветки, которые реальные обходы ФС не дают
// детерминированно).

package osfs

import (
	"io/fs"
	"testing"
	"time"
)

// fakeInfo — FileInfo с управляемым Sys().
type fakeInfo struct {
	sys any
}

func (f fakeInfo) Name() string       { return "f" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return f.sys }

// linuxStat — раскладка syscall.Stat_t Linux (Dev/Ino).
type linuxStat struct {
	Dev uint64
	Ino uint64
}

// winStat — раскладка syscall.FileIDInfo Windows.
type winStat struct {
	VolumeSerialNumber uint32
	FileIndexHigh      uint32
	FileIndexLow       uint32
}

// emptyStat — структура без знакомых полей.
type emptyStat struct {
	X int
}

func TestLinkID_Fallbacks(t *testing.T) {
	cases := []struct {
		name string
		info fakeInfo
		want string
	}{
		{"nil Sys", fakeInfo{sys: nil}, ""},
		{"non-struct Sys", fakeInfo{sys: 42}, ""},
		{"struct without inode fields", fakeInfo{sys: emptyStat{X: 1}}, ""},
		{"linux Dev/Ino", fakeInfo{sys: linuxStat{Dev: 5, Ino: 9}}, "5:9"},
		{"windows file id", fakeInfo{sys: winStat{VolumeSerialNumber: 1, FileIndexHigh: 2, FileIndexLow: 3}}, "1:2:3"},
		{"pointer to struct", fakeInfo{sys: &linuxStat{Dev: 7, Ino: 8}}, "7:8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkID(tc.info); got != tc.want {
				t.Fatalf("linkID = %q; want %q", got, tc.want)
			}
		})
	}
}
