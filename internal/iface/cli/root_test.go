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

package cli_test

import (
	"testing"

	"lentovodec/internal/iface/cli"
)

// TestDefaultDepsSmoke — продакшн-wiring собирается, чистые функции
// зависимостей работают (системные адаптеры — тонкие обёртки os/time).
func TestDefaultDepsSmoke(t *testing.T) {
	deps := cli.DefaultDeps("test")
	if deps.Version != "test" || deps.FS == nil || deps.Codec == nil ||
		deps.OpenConfig == nil || deps.OpenTape == nil || deps.OpenCatalog == nil ||
		deps.DialServer == nil || deps.NewDaemon == nil {
		t.Fatalf("DefaultDeps собран не полностью: %+v", deps)
	}
	if deps.Clock.Now().IsZero() {
		t.Error("Clock.Now: нулевое время")
	}
	id, err := deps.Rand.UUID4()
	if err != nil || len(id) != 36 {
		t.Errorf("Rand.UUID4 = %q, %v", id, err)
	}
}
