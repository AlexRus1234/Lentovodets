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

package tomlconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"lentovodec/internal/adapter/tomlconfig"
)

// TestSource — путь и mtime файла конфигурации для снапшота в индексе
// сессии (docs/FORMAT.md §5); отсутствующий файл — пустые значения.
func TestSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lentovodec.toml")
	if err := os.WriteFile(path, []byte("device = \"/dev/nst1\"\n"), 0o644); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	cfg, err := tomlconfig.New(path, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gotPath, mtime := cfg.Source()
	if gotPath != path {
		t.Errorf("path = %q; want %q", gotPath, path)
	}
	if mtime.IsZero() {
		t.Error("mtime = ноль при существующем файле")
	}

	missing, err := tomlconfig.New(filepath.Join(dir, "нет.toml"), nil)
	if err != nil {
		t.Fatalf("New без файла: %v", err)
	}
	gotPath, mtime = missing.Source()
	if gotPath != "" || !mtime.IsZero() {
		t.Errorf("Source без файла = %q, %v; want пустые значения", gotPath, mtime)
	}
}
