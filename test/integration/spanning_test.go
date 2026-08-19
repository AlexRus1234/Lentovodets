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

// Сквозные сценарии spanning (сессия 8 плана «тома», Этап 11): дозапись
// с делением на две кассеты, ENOSPC-перенос части, restore full по
// цепочке (DR и с каталогом), контракт DR «голый tar», mirror-реконструкция
// по цепочке. Все зависимости реальные (filetape + osfs + sqlite +
// tapeformat); смена кассет — авто-фабрика spanFarm (FuncChanger).

package integration

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"lentovodec/internal/adapter/filetape"
	"lentovodec/internal/adapter/sqlite"
	"lentovodec/internal/domain"
	"lentovodec/internal/iface/destfs"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/backup"
	"lentovodec/internal/usecase/catalog"
	"lentovodec/internal/usecase/format"
	"lentovodec/internal/usecase/restore"
)

// spanFarm — FuncChanger-подобная авто-фабрика кассет поверх filetape:
// каждая кассета — свежий файл-лента в базовом каталоге харнесса.
// Режим бекапа (newChanger) форматирует кассету и регистрирует её в
// каталоге; режим чтения (serveChanger) выдаёт существующую по имени
// из указателя продолжения (restore/readtest, кассета не форматируется).
type spanFarm struct {
	h      *harness
	paths  map[string]string // имя кассеты → путь файла-ленты
	labels map[string]domain.TapeLabel
	opened []*filetape.Tape // выданные ленты; закрываются cleanup'ом
}

// spanFarmOf создаёт фабрику кассет для харнесса.
func (h *harness) spanFarmOf() *spanFarm {
	f := &spanFarm{
		h:      h,
		paths:  map[string]string{},
		labels: map[string]domain.TapeLabel{},
	}
	h.t.Cleanup(func() {
		for _, tp := range f.opened {
			_ = tp.Close()
		}
	})
	return f
}

// path — путь файла-ленты кассеты name.
func (f *spanFarm) path(name string) string {
	return filepath.Join(filepath.Dir(f.h.tapePath), "span-"+name+".dat")
}

// openTapeFile открывает файл-ленту (с лимитом ёмкости харнесса, если
// задан) и учит её в фабрике.
func (f *spanFarm) openTapeFile(path string) (*filetape.Tape, error) {
	var (
		tp  *filetape.Tape
		err error
	)
	if f.h.tapeCapacity > 0 {
		tp, err = filetape.OpenCapacity(path, f.h.tapeCapacity)
	} else {
		tp, err = filetape.Open(path)
	}
	if err != nil {
		return nil, err
	}
	f.opened = append(f.opened, tp)
	return tp, nil
}

// newChanger — changer бекапа: следующая кассета — свежая, форматируется
// и регистрируется в каталоге (как stdinChanger CLI, сессия 5).
func (f *spanFarm) newChanger() *testutil.FuncChanger {
	return &testutil.FuncChanger{
		Request: func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			tp, err := f.openTapeFile(f.path(req.NextTapeName))
			if err != nil {
				return nil, domain.TapeLabel{}, err
			}
			label, err := format.New(tp, f.h.codec, f.h.cat, f.h.rnd, f.h.clock, testutil.NoopLogger()).
				Format(ctx, req.NextTapeName, false)
			if err != nil {
				return nil, domain.TapeLabel{}, err
			}
			f.paths[label.Name], f.labels[label.Name] = f.path(label.Name), label
			return tp, label, nil
		},
	}
}

// serveChanger — changer чтения цепочки (restore/readtest): открывает
// файл-ленту по имени из указателя продолжения и читает ярлык (как
// restoreChanger CLI; сверку цепочки делает ChainFollower).
func (f *spanFarm) serveChanger() *testutil.FuncChanger {
	return &testutil.FuncChanger{
		Request: func(ctx context.Context, req port.NextTapeRequest) (port.Tape, domain.TapeLabel, error) {
			path, ok := f.paths[req.NextTapeName]
			if !ok {
				return nil, domain.TapeLabel{},
					fmt.Errorf("spanFarm: кассета %q не создавалась фабрикой", req.NextTapeName)
			}
			tp, err := f.openTapeFile(path)
			if err != nil {
				return nil, domain.TapeLabel{}, err
			}
			if err := tp.Rewind(ctx); err != nil {
				return nil, domain.TapeLabel{}, err
			}
			block, err := tp.ReadBlock(ctx)
			if err != nil {
				return nil, domain.TapeLabel{}, fmt.Errorf("spanFarm: чтение ярлыка %s: %w", req.NextTapeName, err)
			}
			label, err := f.h.codec.DecodeLabel(block)
			if err != nil {
				return nil, domain.TapeLabel{}, err
			}
			return tp, label, nil
		},
	}
}

// backupSpanUC — backup.UseCase с changer'ом; capacity/min_tail задаются
// в h.cfg (StaticConfig) до вызова.
func (h *harness) backupSpanUC(ch port.TapeChanger) *backup.UseCase {
	return backup.New(h.cfg, h.tape, h.codec, h.cat, h.fs, h.hasher,
		h.rnd, h.clock, nil, testutil.NoopLogger(), ch)
}

// restoreChainUC — restore.UseCase со следованием цепочке кассет
// (каталог cat не обязателен — DR-семантика).
func (h *harness) restoreChainUC(dest string, ch port.TapeChanger, cat port.Catalog) *restore.UseCase {
	return restore.New(h.tape, h.codec, cat, destfs.Wrap(h.fs, dest),
		nil, testutil.NoopLogger(), ch)
}

// catalogChainUC — catalog.UseCase с changer'ом (readtest по цепочке).
func (h *harness) catalogChainUC(ch port.TapeChanger) *catalog.UseCase {
	return catalog.New(h.cat, h.tape, h.codec, nil, testutil.NoopLogger(), ch)
}

// remountTape1 ставит в харнесс свежий дескриптор первой кассеты:
// прежний закрыт changer'ом при смене, повторное закрытие невозможно.
// Аналог установки кассеты в привод новым процессом.
func (h *harness) remountTape1() {
	h.t.Helper()
	tp, err := filetape.Open(h.tapePath)
	if err != nil {
		h.t.Fatalf("filetape.Open(tape-1): %v", err)
	}
	h.tape = tp
}

// filetapeMagic — magic-заголовок файла-ленты (adapter/filetape).
const filetapeMagic = "FTAPEV1\x00"

// countFilemarks считает filemark-фреймы файла-ленты. EOD-инвариант
// FORMAT §4: лента с K сессиями содержит 2K+3 метки; с continuation-
// хвостом их столько же — блок-указатель становится последней записью
// данных перед закрывающей парой.
func countFilemarks(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение файла ленты %s: %v", path, err)
	}
	if len(raw) < len(filetapeMagic) || string(raw[:len(filetapeMagic)]) != filetapeMagic {
		t.Fatalf("%s: не файл-лента filetape", path)
	}
	marks, i := 0, len(filetapeMagic)
	for i < len(raw) {
		switch raw[i] {
		case 0x01: // блок данных: тип | uint32le len | payload
			i += 5 + int(binary.LittleEndian.Uint32(raw[i+1:i+5]))
		case 0x02: // filemark
			marks++
			i += 5
		default:
			t.Fatalf("%s: неизвестный фрейм 0x%02x на смещении %d", path, raw[i], i)
		}
	}
	return marks
}

// continuationAt читает блок-указатель продолжения с позиции после
// filemark'а № skipMarks (FORMAT §9: позиция за tar последней сессии —
// MTFSF(2K+1) для ленты с K сессиями).
func continuationAt(t *testing.T, h *harness, tapePath string, skipMarks int) port.Continuation {
	t.Helper()
	tp, err := filetape.Open(tapePath)
	if err != nil {
		t.Fatalf("filetape.Open: %v", err)
	}
	defer func() { _ = tp.Close() }()
	ctx := context.Background()
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("перемотка: %v", err)
	}
	if err := tp.ForwardFilemarks(ctx, skipMarks); err != nil {
		t.Fatalf("MTFSF(%d): %v", skipMarks, err)
	}
	cont, err := h.codec.ReadContinuation(ctx, tp)
	if err != nil {
		t.Fatalf("ReadContinuation: %v", err)
	}
	return cont
}

// firstHeaderAt читает заголовок сессии 1 (индекс без tar) — проверка
// обратной ссылки continues части на новой кассете.
func firstHeaderAt(t *testing.T, h *harness, tapePath string) port.SessionHeader {
	t.Helper()
	tp, err := filetape.Open(tapePath)
	if err != nil {
		t.Fatalf("filetape.Open: %v", err)
	}
	defer func() { _ = tp.Close() }()
	ctx := context.Background()
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("перемотка: %v", err)
	}
	if err := tp.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatalf("MTFSF(1): %v", err)
	}
	hdr, err := h.codec.ReadHeader(ctx, tp)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	return hdr
}

// appendSplitSetup — кассета с сессией (seed.bin) и второй запуск, не
// влезающий в остаток: часть 1 в остаток LTO-001, часть 2 на LTO-002.
func appendSplitSetup(t *testing.T) (*harness, *spanFarm, *testutil.FuncChanger, backup.Result, domain.TapeLabel) {
	t.Helper()
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	h.cfg.CapacityBytes = 1_000_000 // бюджет дозаписи: 1M − 300k = 700k
	h.cfg.MinTailBytes = 1000
	ctx := context.Background()

	label, err := h.formatUC().Format(ctx, "LTO-001", false)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "seed.bin", bytes.Repeat([]byte{0x5A}, 300_000))
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("backup #1: %v", err)
	}

	farm := h.spanFarmOf()
	ch := farm.newChanger()
	writeFile(t, h.src, "data/a.dat", bytes.Repeat([]byte{0xA1}, 400_000))
	writeFile(t, h.src, "data/b.dat", bytes.Repeat([]byte{0xB2}, 400_000))
	res, err := h.backupSpanUC(ch).Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup #2 (spanning): %v", err)
	}
	return h, farm, ch, res, label
}

// TestSpanning_AppendSplitsIntoRemainder — дозапись с делением:
// часть 1 занимает остаток кассеты, часть 2 уходит на новую; проверяются
// цепочка GetSessionChain, указатель продолжения на ленте-1, EOD-инварианты
// обеих лент и заголовок части на новой кассете.
func TestSpanning_AppendSplitsIntoRemainder(t *testing.T) {
	h, farm, ch, res, label := appendSplitSetup(t)
	ctx := context.Background()

	if res.Parts != 2 || res.PlannedParts != 2 || !res.PlannedByBudget {
		t.Fatalf("итог: parts=%d/%d byBudget=%v; хочу 2/2 true", res.Parts, res.PlannedParts, res.PlannedByBudget)
	}
	if res.Session.Num != 2 || res.Session.Type != domain.SessionInc || res.Session.Part != 1 {
		t.Fatalf("сессия части 1: %+v; хочу num=2 INC part=1 (дозапись в остаток)", res.Session)
	}
	if got := strings.Join(res.Tapes, ","); got != "LTO-001,LTO-002" {
		t.Fatalf("Tapes = %q; хочу LTO-001,LTO-002", got)
	}

	// цепочка запуска в каталоге: обе части с общим JobRunID
	chain, err := h.cat.GetSessionChain(ctx, res.Session.JobRunID)
	if err != nil {
		t.Fatalf("GetSessionChain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("цепочка: %d частей; хочу 2", len(chain))
	}
	if chain[0].Part != 1 || chain[0].TapeUUID != label.UUID || chain[0].Num != 2 ||
		chain[1].Part != 2 || chain[1].Num != 1 || chain[1].Type != domain.SessionFull {
		t.Fatalf("цепочка: %+v", chain)
	}

	// указатель продолжения на ленте-1: позиция за tar части 1
	// (лента с K=2 сессиями: MTFSF(2K+1)=5)
	cont := continuationAt(t, h, h.tapePath, 5)
	if cont.JobRunID != res.Session.JobRunID || cont.SessionNum != 2 ||
		cont.Part != 2 || cont.NextTapeName != "LTO-002" {
		t.Fatalf("указатель продолжения: %+v", cont)
	}

	// EOD-инварианты обеих лент (FORMAT §4: 2K+3 меток)
	if marks := countFilemarks(t, h.tapePath); marks != 2*2+3 {
		t.Errorf("лента-1: filemark'ов %d; хочу %d (2K+3, K=2, continuation-хвост)", marks, 2*2+3)
	}
	tape2 := farm.paths["LTO-002"]
	if marks := countFilemarks(t, tape2); marks != 2*1+3 {
		t.Errorf("лента-2: filemark'ов %d; хочу %d (2K+3, K=1)", marks, 2*1+3)
	}

	// часть 2 на новой кассете: сессия 1 FULL с обратной ссылкой
	hdr := firstHeaderAt(t, h, tape2)
	if hdr.Part != 2 || hdr.SessionNum != 1 || hdr.Type != domain.SessionFull ||
		hdr.Continues != label.UUID || hdr.JobRunID != res.Session.JobRunID {
		t.Fatalf("заголовок части 2: %+v", hdr)
	}

	// смена кассеты — плановая (span), после части 1
	if len(ch.Requests) != 1 || ch.Closed != 1 {
		t.Fatalf("changer: запросов %d, закрытий %d; хочу 1/1", len(ch.Requests), ch.Closed)
	}
	if req := ch.Requests[0]; req.Reason != port.ReasonSpan || req.Part != 2 ||
		req.FinishedTape != "LTO-001" || req.NextTapeName != "LTO-002" || req.JobName != "daily" {
		t.Fatalf("запрос смены: %+v", req)
	}
}

// TestSpanning_ENOSPCMovesPartToNextTape — аварийный ENOSPC: оценка
// ёмкости не учла накладные расходы (индекс, добивка блоков), фактическая
// лента (лимит filetape) меньше оценки → часть переносится на следующую
// кассету; лента-1 остаётся чистой (readtest), данные целы.
func TestSpanning_ENOSPCMovesPartToNextTape(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	block := int64(domain.BlockSize)
	h.tapeCapacity = 6 * block // фактическая ёмкость — меньше оценки планировщика
	h.reopen()
	h.cfg.CapacityBytes = 6 * block // оценка планировщика: budget = 6B − B(seed) = 5B
	h.cfg.MinTailBytes = 1
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-001", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "seed.bin", bytes.Repeat([]byte{0x33}, int(block)))
	if _, err := h.backupUC().Backup(ctx, "daily", backup.Options{}); err != nil {
		t.Fatalf("backup #1: %v", err)
	}

	// По планировщику одна часть (2.5B ≤ 5B), но индекс + tar с добивкой
	// до полных блоков не влезают в фактический остаток (~2B) → ENOSPC
	// посреди записи и перенос части на следующую кассету.
	big := bytes.Repeat([]byte{0x44}, int(5*block/2))
	writeFile(t, h.src, "big.bin", big)
	farm := h.spanFarmOf()
	ch := farm.newChanger()
	res, err := h.backupSpanUC(ch).Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup #2 (ENOSPC-перенос): %v", err)
	}
	if res.Parts != 1 || res.PlannedParts != 1 || !res.PlannedByBudget {
		t.Fatalf("итог: parts=%d/%d byBudget=%v; хочу 1/1 true", res.Parts, res.PlannedParts, res.PlannedByBudget)
	}
	if len(ch.Requests) != 1 || ch.Requests[0].Reason != port.ReasonEnospc {
		t.Fatalf("запросы смены: %+v; хочу один enospc", ch.Requests)
	}

	// лента-1 чистая: одна сессия + указатель продолжения,
	// EOD-инвариант 2K+3 (K=1)
	cont := continuationAt(t, h, h.tapePath, 3) // MTFSF(2*1+1)
	if cont.JobRunID != res.Session.JobRunID || cont.SessionNum != 2 ||
		cont.Part != 1 || cont.NextTapeName != "LTO-002" {
		t.Fatalf("указатель продолжения: %+v", cont)
	}
	if marks := countFilemarks(t, h.tapePath); marks != 2*1+3 {
		t.Errorf("лента-1: filemark'ов %d; хочу 5 (2K+3, K=1)", marks)
	}

	// цепочка запуска — одна часть, целиком на новой кассете
	chain, err := h.cat.GetSessionChain(ctx, res.Session.JobRunID)
	if err != nil {
		t.Fatalf("GetSessionChain: %v", err)
	}
	if len(chain) != 1 || chain[0].Part != 1 || chain[0].TapeUUID != farm.labels["LTO-002"].UUID {
		t.Fatalf("цепочка: %+v; хочу 1 часть на LTO-002", chain)
	}

	// readtest по цепочке: лента-1 — только прошлая сессия (грязного
	// хвоста нет), перенесённая часть — на ленте-2
	h.remountTape1()
	reports, err := h.catalogChainUC(farm.serveChanger()).ReadTest(ctx)
	if err != nil {
		t.Fatalf("readtest: %v", err)
	}
	want := []catalog.TapeReport{
		{Name: "LTO-001", Sessions: 1, Files: 1, Bytes: block},
		{Name: "LTO-002", Sessions: 1, Files: 1, Bytes: int64(len(big))},
	}
	if len(reports) != 2 {
		t.Fatalf("readtest: отчёт %+v; хочу 2 кассеты", reports)
	}
	for i := range want {
		if reports[i] != want[i] {
			t.Errorf("readtest отчёт[%d]: %+v; хочу %+v", i, reports[i], want[i])
		}
	}

	// данные целы: restore full по цепочке восстанавливает всё дерево
	h.remountTape1()
	wantFiles, wantDirs := treeOf(t, h.src)
	st, err := h.restoreChainUC(h.dest, farm.serveChanger(), h.cat).Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	if st.Sessions != 2 || st.Files != len(wantFiles) {
		t.Fatalf("restore stats: %+v; хочу sessions=2 files=%d", st, len(wantFiles))
	}
	assertTreeEqual(t, destRoot(h.dest, h.src), wantFiles, wantDirs)
}

// TestSpanning_RestoreFullFollowsChain — restore full по цепочке кассет:
// с пустым каталогом (DR — только ленты и указатели) и с каталогом;
// всё дерево побайтово.
func TestSpanning_RestoreFullFollowsChain(t *testing.T) {
	h, farm, _, _, _ := appendSplitSetup(t)
	ctx := context.Background()
	wantFiles, wantDirs := treeOf(t, h.src)

	// DR: пустой каталог, цепочка читается с самих лент
	h.remountTape1()
	drCat, err := sqlite.New(filepath.Join(filepath.Dir(h.tapePath), "dr.db"))
	if err != nil {
		t.Fatalf("sqlite.New(dr): %v", err)
	}
	defer func() { _ = drCat.Close() }()
	st, err := h.restoreChainUC(h.dest, farm.serveChanger(), drCat).Full(ctx)
	if err != nil {
		t.Fatalf("restore full (DR): %v", err)
	}
	if st.Sessions != 3 || st.Files != len(wantFiles) {
		t.Fatalf("restore DR: %+v; хочу sessions=3 files=%d", st, len(wantFiles))
	}
	assertTreeEqual(t, destRoot(h.dest, h.src), wantFiles, wantDirs)

	// с каталогом — тот же результат
	h.remountTape1()
	dest2 := h.dest + "-cat"
	st2, err := h.restoreChainUC(dest2, farm.serveChanger(), h.cat).Full(ctx)
	if err != nil {
		t.Fatalf("restore full (каталог): %v", err)
	}
	if st2.Sessions != 3 || st2.Files != len(wantFiles) {
		t.Fatalf("restore с каталогом: %+v; хочу sessions=3 files=%d", st2, len(wantFiles))
	}
	assertTreeEqual(t, destRoot(dest2, h.src), wantFiles, wantDirs)
}

// bareTarExtract читает tar-сегмент первой сессии кассеты так, как это
// сделает оператор по DR-рецепту (FORMAT §12): Rewind → FSF(2) → чтение
// блоков до filemark (= `dd bs=256k`), распаковка archive/tar — без
// лентоводеческих декодеров. Возвращает файлы (путь относительно корня
// задания → содержимое).
func bareTarExtract(t *testing.T, h *harness, tapePath string) map[string][]byte {
	t.Helper()
	tp, err := filetape.Open(tapePath)
	if err != nil {
		t.Fatalf("filetape.Open: %v", err)
	}
	defer func() { _ = tp.Close() }()
	ctx := context.Background()
	if err := tp.Rewind(ctx); err != nil {
		t.Fatalf("перемотка: %v", err)
	}
	if err := tp.ForwardFilemarks(ctx, 2); err != nil {
		t.Fatalf("MTFSF(2): %v", err)
	}
	var stream bytes.Buffer
	for {
		block, err := tp.ReadBlock(ctx)
		if errors.Is(err, io.EOF) {
			break // filemark после tar-сегмента — dd здесь останавливается
		}
		if err != nil {
			t.Fatalf("ReadBlock: %v", err)
		}
		stream.Write(block)
	}
	files := make(map[string][]byte)
	prefix := filepath.ToSlash(filepath.Clean(h.src)) + "/"
	tr := tar.NewReader(&stream)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel := strings.TrimPrefix(filepath.ToSlash(hdr.Name), prefix)
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar %s: %v", rel, err)
		}
		files[rel] = raw
	}
	return files
}

// TestSpanning_BareTarDRContract — каждая кассета цепочки читается голым
// GNU tar: tar-сегмент самодостаточен, файлы части восстанавливаются
// без лентоводеческих декодеров (автоматический эквивалент рецепта
// `mt fsf 2 && dd bs=256k | tar -x`).
func TestSpanning_BareTarDRContract(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "daily", Mode: domain.ModeAppend, Paths: []string{h.src}})
	h.cfg.CapacityBytes = 500_000 // первая сессия: f1 в часть 1, f2 — в часть 2
	h.cfg.MinTailBytes = 1000
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-001", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	f1 := bytes.Repeat([]byte{0x11}, 300_000)
	f2 := bytes.Repeat([]byte{0x22}, 300_000)
	writeFile(t, h.src, "f1.bin", f1)
	writeFile(t, h.src, "f2.bin", f2)
	farm := h.spanFarmOf()
	res, err := h.backupSpanUC(farm.newChanger()).Backup(ctx, "daily", backup.Options{})
	if err != nil {
		t.Fatalf("backup (spanning): %v", err)
	}
	if res.Parts != 2 {
		t.Fatalf("частей %d; хочу 2", res.Parts)
	}

	part1 := bareTarExtract(t, h, h.tapePath)
	part2 := bareTarExtract(t, h, farm.paths["LTO-002"])
	if len(part1) != 1 || !bytes.Equal(part1["f1.bin"], f1) {
		t.Errorf("лента-1: bare-tar файлы %v; хочу один f1.bin с исходным содержимым", keysOf(part1))
	}
	if len(part2) != 1 || !bytes.Equal(part2["f2.bin"], f2) {
		t.Errorf("лента-2: bare-tar файлы %v; хочу один f2.bin с исходным содержимым", keysOf(part2))
	}
}

// TestSpanning_SpanDepthDirectoryGroups — span_depth=1: два поддерева
// режутся по границе каталогов — группа docs (не влезающая в остаток
// после группы movies) откатывается целиком в часть 2 вместе с
// каталогом-главой; файлы частей принадлежат группам своих поддеревьев.
func TestSpanning_SpanDepthDirectoryGroups(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{
		Name: "media", Mode: domain.ModeAppend,
		Paths: []string{h.src}, SpanDepth: 1,
	})
	h.cfg.CapacityBytes = 1_000_000 // movies 750k + docs 550k = 2 части
	h.cfg.MinTailBytes = 1000
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-001", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "movies/m1.bin", bytes.Repeat([]byte{0x11}, 400_000))
	writeFile(t, h.src, "movies/m2.bin", bytes.Repeat([]byte{0x22}, 350_000))
	writeFile(t, h.src, "docs/d1.bin", bytes.Repeat([]byte{0x33}, 300_000))
	writeFile(t, h.src, "docs/d2.bin", bytes.Repeat([]byte{0x44}, 250_000))

	farm := h.spanFarmOf()
	res, err := h.backupSpanUC(farm.newChanger()).Backup(ctx, "media", backup.Options{})
	if err != nil {
		t.Fatalf("backup (span_depth=1): %v", err)
	}
	if res.Parts != 2 || res.PlannedParts != 2 {
		t.Fatalf("итог: parts=%d/%d; хочу 2/2", res.Parts, res.PlannedParts)
	}

	chain, err := h.cat.GetSessionChain(ctx, res.Session.JobRunID)
	if err != nil {
		t.Fatalf("GetSessionChain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("цепочка: %d частей; хочу 2", len(chain))
	}
	prefix := filepath.ToSlash(filepath.Clean(h.src))
	partFiles := make([][]string, 2)
	for i, sess := range chain {
		files, err := h.cat.GetFilesBySession(ctx, sess.ID)
		if err != nil {
			t.Fatalf("файлы части %d: %v", i+1, err)
		}
		for _, fm := range files {
			partFiles[i] = append(partFiles[i], strings.TrimPrefix(fm.Path, prefix+"/"))
		}
	}
	// обход лексический: docs идут первыми и заполняют часть 1
	// (src + группа docs, 550k), группа movies (750k) не влезает в
	// остаток 450k и откатывается целиком в часть 2 с каталогом-главой
	if len(partFiles[0]) != 4 || partFiles[0][1] != "docs" ||
		partFiles[0][2] != "docs/d1.bin" || partFiles[0][3] != "docs/d2.bin" {
		t.Errorf("часть 1 = %v; хочу корень + группу docs целиком", partFiles[0])
	}
	if len(partFiles[1]) != 3 || partFiles[1][0] != "movies" ||
		partFiles[1][1] != "movies/m1.bin" || partFiles[1][2] != "movies/m2.bin" {
		t.Errorf("часть 2 = %v; хочу группу movies целиком с каталогом-главой", partFiles[1])
	}

	// restore full по цепочке — дерево целиком, локальность не потеряла
	h.remountTape1()
	wantFiles, wantDirs := treeOf(t, h.src)
	st, err := h.restoreChainUC(h.dest, farm.serveChanger(), h.cat).Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	if st.Files != len(wantFiles) {
		t.Fatalf("restore stats: %+v; хочу files=%d", st, len(wantFiles))
	}
	assertTreeEqual(t, destRoot(h.dest, h.src), wantFiles, wantDirs)
}

// keysOf возвращает отсортированный список ключей карты (для сообщений
// об ошибке).
func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSpanning_MirrorReconstructsAcrossChain — mirror + spanning:
// tombstone удалённого файла попадает в часть цепочки; restore full по
// цепочке реконструирует зеркало каталога (удалённый файл исчезает из
// dest, изменения и новые файлы на месте).
func TestSpanning_MirrorReconstructsAcrossChain(t *testing.T) {
	h := newHarness(t)
	h.addJob(domain.Job{Name: "media", Mode: domain.ModeMirror, Paths: []string{h.src}})
	h.cfg.CapacityBytes = 800_000 // бюджет: 800k − 150 = 799850; e.bin (500k) не влезает с d.bin
	h.cfg.MinTailBytes = 1
	ctx := context.Background()

	if _, err := h.formatUC().Format(ctx, "LTO-001", false); err != nil {
		t.Fatalf("format: %v", err)
	}
	writeFile(t, h.src, "a.txt", bytes.Repeat([]byte{'a'}, 60))
	writeFile(t, h.src, "b.txt", bytes.Repeat([]byte{'b'}, 50))
	writeFile(t, h.src, "c.txt", bytes.Repeat([]byte{'c'}, 40))
	if _, err := h.backupUC().Backup(ctx, "media", backup.Options{}); err != nil {
		t.Fatalf("backup #1: %v", err)
	}

	// v2: a.txt изменён, b.txt удалён, d/e добавлены; часть 2 — e.bin
	// вместе с tombstone b.txt
	writeFile(t, h.src, "a.txt", bytes.Repeat([]byte{'A'}, 80))
	if err := os.Remove(filepath.Join(h.src, "b.txt")); err != nil {
		t.Fatalf("удаление b.txt: %v", err)
	}
	d := bytes.Repeat([]byte{0x0D}, 400_000)
	e := bytes.Repeat([]byte{0x0E}, 500_000)
	writeFile(t, h.src, "d.bin", d)
	writeFile(t, h.src, "e.bin", e)
	farm := h.spanFarmOf()
	res, err := h.backupSpanUC(farm.newChanger()).Backup(ctx, "media", backup.Options{})
	if err != nil {
		t.Fatalf("backup #2 (spanning): %v", err)
	}
	if res.Parts != 2 {
		t.Fatalf("частей %d; хочу 2", res.Parts)
	}

	// tombstone b.txt — во второй части
	chain, err := h.cat.GetSessionChain(ctx, res.Session.JobRunID)
	if err != nil {
		t.Fatalf("GetSessionChain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("цепочка: %d частей; хочу 2", len(chain))
	}
	part2files, err := h.catalogUC().GetFiles(ctx, chain[1].ID)
	if err != nil {
		t.Fatalf("файлы части 2: %v", err)
	}
	tombstones := map[string]bool{}
	for _, fm := range part2files {
		if fm.IsDeleted() {
			tombstones[fm.Path] = true
		}
	}
	if len(tombstones) != 1 || !hasSuffixPath(tombstones, "b.txt") {
		t.Fatalf("tombstone'ы части 2: %v; хочу только b.txt", tombstones)
	}

	// restore full по цепочке: зеркало реконструировано
	h.remountTape1()
	st, err := h.restoreChainUC(h.dest, farm.serveChanger(), h.cat).Full(ctx)
	if err != nil {
		t.Fatalf("restore full: %v", err)
	}
	if st.Sessions != 3 {
		t.Fatalf("restore stats: %+v; хочу sessions=3 (v1, часть 1, часть 2)", st)
	}
	gotFiles, _ := treeOf(t, destRoot(h.dest, h.src))
	want := map[string][]byte{
		"a.txt": bytes.Repeat([]byte{'A'}, 80),
		"c.txt": bytes.Repeat([]byte{'c'}, 40),
		"d.bin": d,
		"e.bin": e,
	}
	if len(gotFiles) != len(want) {
		t.Fatalf("в dest %d файлов, хочу %d: %v", len(gotFiles), len(want), keysOf(gotFiles))
	}
	for p, wantContent := range want {
		if got, ok := gotFiles[p]; !ok || !bytes.Equal(got, wantContent) {
			t.Errorf("файл %s: совпал=%v; хочу исходное содержимое v2", p, ok)
		}
	}
	if _, ok := gotFiles["b.txt"]; ok {
		t.Error("удалённый b.txt восстановился: tombstone части цепочки не сработал")
	}
}
