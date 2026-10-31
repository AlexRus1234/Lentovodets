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

// Юнит-проверка снапшот-полей заголовка против FakeCodec (кодек пишет
// заголовок как есть). Дедуп на реальном кодеке — test/integration.

package backup_test

import (
	"context"
	"reflect"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/usecase/backup"
)

// TestBackup_SnapshotFieldsInHeader — первая сессия кассеты несёт
// полное тело снапшота, его канонический хеш и версию инструмента
// (docs/FORMAT.md §5); источник конфигурации у fakeConfig неизвестен.
func TestBackup_SnapshotFieldsInHeader(t *testing.T) {
	h := newHarness(t, map[string]string{"etc/hosts": "x"})
	if _, err := h.uc.Backup(context.Background(), "daily", backup.Options{}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if len(h.codec.WroteHeaders) != 1 {
		t.Fatalf("WriteSession вызовов %d; want 1", len(h.codec.WroteHeaders))
	}
	hdr := h.codec.WroteHeaders[0]
	job := jobsAppend()[0]
	if hdr.JobConfig == nil {
		t.Fatal("первая сессия без полного тела снапшота")
	}
	if !reflect.DeepEqual(*hdr.JobConfig, job) {
		t.Errorf("тело снапшота: %+v; want %+v", *hdr.JobConfig, job)
	}
	if hdr.JobConfigHash != domain.JobConfigHash(job) {
		t.Error("job_config_hash не равен хешу канонической формы задания")
	}
	if hdr.ToolVersion != "test-version" {
		t.Errorf("tool_version = %q; want test-version", hdr.ToolVersion)
	}
	if hdr.ConfigSource != "" || hdr.ConfigMTime != 0 {
		t.Errorf("config_source/mtime: %q/%d; want пусто", hdr.ConfigSource, hdr.ConfigMTime)
	}
}
