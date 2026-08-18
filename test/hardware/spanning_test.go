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

//go:build tape && linux

// Ручной spanning-сценарий на двух реальных кассетах (сессия 8 плана
// «тома», Этап 11): capacity в конфиге заведомо меньше реальной ёмкости —
// бекап делится на части, смена кассеты по промпту, восстановление цепочки.
//
// Запуск (две чистые кассеты, машина с приводом, терминал, пользователь
// в группе tape):
//
//	LENTOVODEC_TAPE_DEVICE=/dev/nst0 \
//	LENTOVODEC_TAPE_SPAN_CAPACITY=64M \
//	go test -tags=tape ./test/hardware/ -v -count=1 -run TestHardware_SpanningTwoTapes
//
// LENTOVODEC_TAPE_SPAN_CAPACITY — оценка ёмкости для планировщика
// (по умолчанию 64M): общий объём данных должен её превышать, реальная
// лента — вмещать каждую часть. Для повторного запуска на использованных
// кассетах — LENTOVODEC_TAPE_REFORMAT=1.
//
// Ход сценария: format → spanning-бекап (две части, промпт на смену) →
// readtest по цепочке → restore full по цепочке → побайтовое сравнение.
// Промпты печатаются в stderr, подтверждение — Enter в stdin.
//
// DR-проверка фактическим mt/dd/tar — оператор выполняет вручную после
// теста (см. docs/FORMAT.md §12), для каждой кассеты цепочки:
//
//	mt -f /dev/nst0 rewind
//	mt -f /dev/nst0 fsf 2
//	dd if=/dev/nst0 bs=256k | tar -tv    # список файлов части
//	dd if=/dev/nst0 bs=256k | tar -x     # извлечение (в отдельный каталог)
//
// tar-сегмент каждой кассеты самодостаточен: часть восстанавливается без
// лентоводеческих декодеров. Автоматический эквивалент этой проверки —
// test/integration/spanning_test.go (TestSpanning_BareTarDRContract).

package hardware

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lentovodec/internal/adapter/linuxtape"
	"lentovodec/internal/adapter/osfs"
	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/adapter/xxhash"
	"lentovodec/internal/domain"
	"lentovodec/internal/iface/destfs"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
	"lentovodec/internal/usecase/catalog"
	"lentovodec/internal/usecase/format"
	"lentovodec/internal/usecase/restore"
)

// hwPrompt печатает сообщение оператору в stderr и ждёт Enter в stdin.
func hwPrompt(t *testing.T, format string, args ...any) {
	t.Helper()
	fmt.Fprintf(os.Stderr, format, args...)
	if !bufio.NewScanner(os.Stdin).Scan() {
		t.Fatalf("ввод оператора: %v", bufio.NewScanner(os.Stdin).Err())
	}
}

// hwReadLabel читает ярлык установленной кассеты (сверка цепочки).
func hwReadLabel(ctx context.Context, tp port.Tape, codec port.TapeCodec) (domain.TapeLabel, error) {
	if err := tp.Rewind(ctx); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("перемотка: %w", err)
	}
	block, err := tp.ReadBlock(ctx)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("чтение ярлыка: %w", err)
	}
	label, err := codec.DecodeLabel(block)
	if err != nil {
		return domain.TapeLabel{}, fmt.Errorf("разбор ярлыка: %w", err)
	}
	return label, nil
}

// TestHardware_SpanningTwoTapes — двухкассетный сценарий: планировщик
// делит первую же сессию на две части (capacity меньше реальной ёмкости),
// смена кассеты по промпту, readtest и restore full по цепочке.
func TestHardware_SpanningTwoTapes(t *testing.T) {
	dev := devicePath(t)
	tp := openTape(t)
	ctx := context.Background()

	capStr := os.Getenv("LENTOVODEC_TAPE_SPAN_CAPACITY")
	if capStr == "" {
		capStr = "64M"
	}
	capacity, err := domain.ParseSize(capStr)
	if err != nil {
		t.Fatalf("LENTOVODEC_TAPE_SPAN_CAPACITY=%q: %v", capStr, err)
	}

	// Данные: две части по ~3/5 capacity — вместе больше, по отдельности
	// меньше реальной ленты.
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dest := filepath.Join(base, "dest")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll src: %v", err)
	}
	part1 := bytes.Repeat([]byte{0xA5, 0x5A, 0x00, 0xFF}, int(capacity*3/5/4))
	part2 := bytes.Repeat([]byte{0x0F, 0xF0, 0x11, 0x22}, int(capacity*3/5/4))
	hwWriteFile(t, src, "part1.bin", part1)
	hwWriteFile(t, src, "part2.bin", part2)

	fsys := osfs.New()
	codec := tapeformat.NewCodec()
	hasher := xxhash.New()
	var clock port.Clock = testutil.StepClock(time.Unix(1700000000, 0))
	rnd := testutil.FixedRand(
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
	)
	cfg := &testutil.StaticConfig{
		JobList:       []domain.Job{{Name: "hw", Mode: domain.ModeAppend, Paths: []string{src}}},
		CapacityBytes: capacity,
		MinTailBytes:  1,
	}
	cat, err := sqlite.New(filepath.Join(base, "catalog.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	force := os.Getenv("LENTOVODEC_TAPE_REFORMAT") != ""
	t.Logf("format (force=%v)...", force)
	label, err := format.New(tp, codec, cat, rnd, clock, testutil.NoopLogger()).
		Format(ctx, "HW-SPAN-1", force)
	if err != nil {
		var already *domain.AlreadyFormattedError
		if errors.As(err, &already) {
			t.Fatalf("лента уже отформатирована (%s): запустите с LENTOVODEC_TAPE_REFORMAT=1", already.Name)
		}
		t.Fatalf("format: %v", err)
	}
	t.Logf("formatted: %s (uuid=%s)", label.Name, label.UUID)

	// Changer бекапа: извлечь закрытую кассету, спросить оператора,
	// открыть устройство и отформатировать чистую кассету предложенным
	// именем (HW-SPAN-1 → HW-SPAN-2).
	backupChanger := &testutil.FuncChanger{
		Close: func(ctx context.Context, tape port.Tape) error {
			if err := tape.Eject(ctx); err != nil {
				return fmt.Errorf("извлечение кассеты: %w", err)
			}
			return tape.Close()
		},
		Request: func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			hwPrompt(t, "Кассета %s закрыта. Извлеките её, вставьте ЧИСТУЮ кассету и нажмите Enter: ",
				req.FinishedTape)
			nt, err := linuxtape.Open(dev)
			if err != nil {
				return nil, domain.TapeLabel{}, err
			}
			nl, err := format.New(nt, codec, cat, rnd, clock, testutil.NoopLogger()).
				Format(ctx, req.NextTapeName, false)
			if err != nil {
				_ = nt.Close()
				return nil, domain.TapeLabel{},
					fmt.Errorf("кассета не чиста? вставьте чистую и перезапустите тест: %w", err)
			}
			return nt, nl, nil
		},
	}

	t.Log("backup (spanning, две части)...")
	res, err := backup.New(cfg, tp, codec, cat, fsys, hasher, rnd, clock, nil, testutil.NoopLogger(), backupChanger).
		Backup(ctx, "hw", backup.Options{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if res.Parts != 2 || res.PlannedParts != 2 {
		t.Fatalf("частей %d/%d; хочу 2/2 (проверьте LENTOVODEC_TAPE_SPAN_CAPACITY)", res.Parts, res.PlannedParts)
	}
	if got := fmt.Sprintf("%s,%s", res.Tapes[0], res.Tapes[1]); got != "HW-SPAN-1,HW-SPAN-2" {
		t.Fatalf("Tapes = %v; хочу [HW-SPAN-1 HW-SPAN-2]", res.Tapes)
	}
	t.Logf("backup: частей %d на кассетах %v", res.Parts, res.Tapes)

	// Changer чтения: оператора просят вставить конкретную кассету цепочки
	// (имя известно из указателя продолжения), кассета не форматируется.
	readChanger := &testutil.FuncChanger{
		Request: func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			hwPrompt(t, "Вставьте кассету %s (часть %d) и нажмите Enter: ",
				req.NextTapeName, req.Part)
			nt, err := linuxtape.Open(dev)
			if err != nil {
				return nil, domain.TapeLabel{}, err
			}
			nl, err := hwReadLabel(ctx, nt, codec)
			if err != nil {
				_ = nt.Close()
				return nil, domain.TapeLabel{}, err
			}
			return nt, nl, nil
		},
	}

	t.Log("readtest по цепочке (сначала вставьте кассету 1)...")
	hwPrompt(t, "Вставьте кассету %s и нажмите Enter: ", label.Name)
	tp1, err := linuxtape.Open(dev)
	if err != nil {
		t.Fatalf("linuxtape.Open: %v", err)
	}
	t.Cleanup(func() { _ = tp1.Close() })
	reports, err := catalog.New(cat, tp1, codec, nil, testutil.NoopLogger(), readChanger).ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("readtest: отчёт %+v; хочу 2 кассеты", reports)
	}
	for _, rep := range reports {
		t.Logf("readtest: %s: сессий %d, файлов %d, байт %d", rep.Name, rep.Sessions, rep.Files, rep.Bytes)
		if rep.Sessions != 1 || rep.Files != 1 {
			t.Errorf("readtest %s: %+v; хочу 1 сессию с 1 файлом", rep.Name, rep)
		}
	}

	t.Log("restore full по цепочке...")
	hwPrompt(t, "Вставьте кассету %s и нажмите Enter: ", label.Name)
	tp2, err := linuxtape.Open(dev)
	if err != nil {
		t.Fatalf("linuxtape.Open: %v", err)
	}
	t.Cleanup(func() { _ = tp2.Close() })
	st, err := restore.New(tp2, codec, cat, destfs.Wrap(fsys, dest), nil, testutil.NoopLogger(), readChanger).
		Full(ctx)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Logf("restore: sessions=%d files=%d dirs=%d", st.Sessions, st.Files, st.Dirs)

	want := hwTree(t, src)
	got := hwTree(t, hwDestRoot(dest, src))
	if len(got) != len(want) {
		t.Fatalf("восстановлено файлов %d, хочу %d: %v", len(got), len(want), got)
	}
	for p, wantContent := range want {
		if !bytes.Equal(got[p], wantContent) {
			t.Errorf("файл %s: содержимое не совпало", p)
		}
	}
	t.Log("OK: цепочка восстановлена побайтово")
	t.Log("DR-проверка голым tar (mt/dd/tar) — выполните вручную, рецепт в шапке файла и docs/FORMAT.md §12")
}
