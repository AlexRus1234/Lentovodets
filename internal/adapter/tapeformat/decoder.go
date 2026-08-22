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

// Чтение сессии с ленты: индекс + tar с обязательной проверкой xxhash.
// См. docs/FORMAT.md §6–8.

package tapeformat

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/cespare/xxhash/v2"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// ReadSession читает одну сессию из текущей позиции ленты: индекс, затем
// tar-поток. Возвращает файлы индекса (включая tombstone'ы — восстановление
// mirror трактует их как удаление, docs/FORMAT.md §6). После возврата лента
// стоит за filemark'ом tar-потока — готова к чтению следующей сессии подряд.
// Если на позиции сессии лежит блок-указатель продолжения — ошибка
// *domain.ContinuationError (кассета кончилась, цепочка продолжается
// на следующей).
//
// dest == nil — режим проверки (readtest): содержимое читается и хеши
// сверяются, но на ФС ничего не пишется.
//
// include != nil — выборочное восстановление: на ФС попадают только
// включённые пути (предикат по нормализованному пути индекса); записи
// остальных путей обрабатываются как в режиме проверки — данные читаются
// и хеши сверяются, но не пишутся.
//
// Прогресс: только Update с фазой port.PhaseWrite; Done/Fail публикует
// вызывающий. prog == nil допустим.
func ReadSession(
	ctx context.Context,
	tape port.Tape,
	dest port.FileWriter,
	include func(path string) bool,
	prog port.ProgressReporter,
) ([]domain.FileMeta, error) {
	prog = progressOr(prog)
	idx, err := readIndex(ctx, tape)
	if err != nil {
		return nil, err
	}
	filt := newWriteFilter(include, idx.Files)
	if err := readTar(ctx, tape, dest, idx, filt, prog); err != nil {
		return nil, err
	}
	return idx.Files, nil
}

// ReadHeader читает заголовок сессии с текущей позиции ленты:
// JSON-индекс без списка файлов, tar-поток не читается. После вызова
// позиция — за filemark'ом индекса (как после индексной фазы
// ReadSession). Назначение — сверка цепочки кассет: обратная ссылка
// continues первой сессии новой кассеты проверяется до восстановления
// её данных. Ошибки те же, что у ReadSession на позиции индекса
// (EmptyIndexError, ContinuationError, ...).
func ReadHeader(ctx context.Context, tape port.Tape) (port.SessionHeader, error) {
	idx, err := readIndex(ctx, tape)
	if err != nil {
		return port.SessionHeader{}, err
	}
	return headerOf(idx), nil
}

// ReadIndexFiles читает индекс сессии целиком — заголовок и файлы —
// с текущей позиции ленты, не читая tar-поток: проверка целостности
// данных — работа readtest, а не реконструкции каталога (rebuild).
// После вызова позиция — за filemark'ом индекса, в начале tar-сегмента;
// чтобы встать на индекс следующей сессии, вызывающий пропускает
// filemark tar-сегмента (ForwardFilemarks(1), docs/FORMAT.md §9).
func ReadIndexFiles(ctx context.Context, tape port.Tape) (port.SessionHeader, []domain.FileMeta, error) {
	idx, err := readIndex(ctx, tape)
	if err != nil {
		return port.SessionHeader{}, nil, err
	}
	return headerOf(idx), idx.Files, nil
}

// headerOf проецирует разобранный индекс в заголовок сессии порта.
func headerOf(idx *SessionIndex) port.SessionHeader {
	return port.SessionHeader{
		SessionNum: idx.SessionNum,
		Type:       idx.Type,
		JobRunID:   idx.JobRunID,
		Timestamp:  idx.Timestamp,
		JobName:    idx.JobName,
		Part:       idx.Part,
		Continues:  idx.Continues,
	}
}

// readIndex читает сегмент индекса (блоки до filemark'а) и разбирает JSON.
// На позиции сессии может лежать блок-указатель продолжения — это
// *domain.ContinuationError (кассета кончилась, есть продолжение).
func readIndex(ctx context.Context, tape port.Tape) (*SessionIndex, error) {
	var buf []byte
	for {
		block, err := tape.ReadBlock(ctx)
		if errors.Is(err, io.EOF) {
			break // filemark после индекса
		}
		if err != nil {
			return nil, fmt.Errorf("tapeformat: чтение индекса: %w", err)
		}
		buf = append(buf, block...)
	}
	trimmed := trimZeros(buf)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("tapeformat: %w", &domain.EmptyIndexError{})
	}
	if err := checkContinuation(trimmed); err != nil {
		return nil, err
	}
	var idx SessionIndex
	if err := json.Unmarshal(trimmed, &idx); err != nil {
		return nil, damage(fmt.Errorf("tapeformat: разбор индекса: %w", err))
	}
	if idx.Part < 1 {
		idx.Part = 1 // старые ленты без поля part — все сессии часть 1
	}
	if idx.FormatVersion > domain.FormatVersion {
		return nil, &domain.NewerFormatError{Found: idx.FormatVersion, Supported: domain.FormatVersion}
	}
	if !idx.Type.Valid() {
		return nil, damage(fmt.Errorf("tapeformat: недопустимый тип сессии %q в индексе", idx.Type))
	}
	for i := range idx.Files {
		if err := checkFileMeta(&idx.Files[i]); err != nil {
			return nil, err
		}
	}
	return &idx, nil
}

// checkFileMeta нормализует путь файла индекса и проверяет состояние.
func checkFileMeta(fm *domain.FileMeta) error {
	fm.Path = domain.NormalizePath(fm.Path)
	if !fm.State.Valid() {
		return damage(fmt.Errorf("tapeformat: файл %q в индексе: недопустимое состояние %q", fm.Path, fm.State))
	}
	if err := fm.Validate(); err != nil {
		return damage(fmt.Errorf("tapeformat: %w", err))
	}
	return nil
}

// writeFilter — правила выборочного восстановления: запись попадает на
// ФС, если include истинен для её пути. Хардлинк ссылается на данные
// другой записи (linkname), поэтому для включённых хардлинков их
// компаньоны пишутся тоже — иначе Link бил бы по несозданному файлу.
// nil-фильтр — без фильтрации (восстановление всей сессии).
type writeFilter struct {
	include    func(path string) bool
	companions map[string]bool
}

// newWriteFilter строит фильтр по предикату include и индексу сессии.
func newWriteFilter(include func(path string) bool, files []domain.FileMeta) *writeFilter {
	if include == nil {
		return nil
	}
	var companions map[string]bool
	for _, fm := range files {
		if fm.IsHardlink() && include(fm.Path) {
			if companions == nil {
				companions = make(map[string]bool)
			}
			companions[fm.Linkname] = true
		}
	}
	return &writeFilter{include: include, companions: companions}
}

// writable решает, писать ли запись с путём p на ФС.
func (f *writeFilter) writable(p string) bool {
	if f == nil {
		return true
	}
	return f.include(p) || f.companions[p]
}

// damage помечает ошибку как повреждение данных сессии
// (domain.SessionDamageError): restore full / readtest пропускают
// сессию с Warn, транспортный сбой ленты — нет.
func damage(err error) error {
	return errors.Join(&domain.SessionDamageError{}, err)
}

// readTar читает tar-поток сессии, извлекая файлы в dest (или только
// проверяя хеши, если dest == nil). Невключённые фильтром записи
// обрабатываются так же, как при dest == nil. После цикла каждая
// не-tombstone запись индекса обязана была встретиться в tar —
// урезанный точно по границе записи tar иначе читался бы как успех.
func readTar(ctx context.Context, tape port.Tape, dest port.FileWriter, idx *SessionIndex, filt *writeFilter, prog port.ProgressReporter) error {
	byPath := make(map[string]*domain.FileMeta, len(idx.Files))
	seenInTar := make(map[string]bool, len(idx.Files))
	var total int64
	for i := range idx.Files {
		fm := &idx.Files[i]
		byPath[fm.Path] = fm
		if !fm.IsDeleted() && !fm.IsSymlink() && !fm.IsHardlink() && !fm.IsDir {
			total += fm.Size
		}
	}
	br := &tapeBlockReader{ctx: ctx, tape: tape}
	tr := tar.NewReader(br)
	buf := make([]byte, domain.CopyBuffer)
	var processed int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("tapeformat: чтение tar: %w", err)
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tapeformat: чтение tar: %w", err)
		}
		fm, ok := byPath[domain.NormalizePath(hdr.Name)]
		if !ok {
			return damage(fmt.Errorf("tapeformat: запись tar %q отсутствует в индексе", hdr.Name))
		}
		seenInTar[fm.Path] = true
		entryDest := dest
		if !filt.writable(fm.Path) {
			entryDest = nil // читаем и сверяем хеш, но на ФС не пишем
		}
		if err := extractEntry(ctx, tr, hdr, fm, entryDest, buf, prog, &processed, total); err != nil {
			return err
		}
	}
	if err := verifyIndexCoversTar(idx, seenInTar); err != nil {
		return err
	}
	return drain(br)
}

// verifyIndexCoversTar — каждая не-tombstone запись индекса должна была
// встретиться в tar: урезанный точно по границе записи tar иначе
// читался бы как успех с молча недостающими файлами.
func verifyIndexCoversTar(idx *SessionIndex, seenInTar map[string]bool) error {
	for i := range idx.Files {
		fm := &idx.Files[i]
		if !fm.IsDeleted() && !seenInTar[fm.Path] {
			return damage(fmt.Errorf("tapeformat: файл индекса %q отсутствует в tar", fm.Path))
		}
	}
	return nil
}

// extractEntry обрабатывает одну запись tar: каталог или файл.
func extractEntry(
	ctx context.Context,
	tr *tar.Reader,
	hdr *tar.Header,
	fm *domain.FileMeta,
	dest port.FileWriter,
	buf []byte,
	prog port.ProgressReporter,
	processed *int64,
	total int64,
) error {
	prog.Update(port.ProgressUpdate{
		Phase:          port.PhaseWrite,
		CurrentFile:    fm.Path,
		ProcessedBytes: *processed,
		TotalBytes:     total,
	})
	switch hdr.Typeflag {
	case tar.TypeDir:
		return extractDir(hdr, fm, dest)
	case tar.TypeReg:
		return extractFile(ctx, tr, hdr, fm, dest, buf, prog, processed, total)
	case tar.TypeSymlink:
		return extractSymlink(hdr, fm, dest)
	case tar.TypeLink:
		return extractHardlink(hdr, fm, dest)
	default:
		return damage(fmt.Errorf("tapeformat: запись %q: неожидаемый тип 0x%x в tar", fm.Path, hdr.Typeflag))
	}
}

func extractDir(hdr *tar.Header, fm *domain.FileMeta, dest port.FileWriter) error {
	if dest == nil {
		return nil
	}
	if err := dest.MkdirAll(fm.Path, os.FileMode(hdr.Mode).Perm()); err != nil {
		return fmt.Errorf("tapeformat: создание каталога %q: %w", fm.Path, err)
	}
	return nil
}

func extractSymlink(hdr *tar.Header, fm *domain.FileMeta, dest port.FileWriter) error {
	if !fm.IsSymlink() || hdr.Linkname != fm.Linkname || hdr.Size != 0 {
		return damage(fmt.Errorf("tapeformat: запись %q: неожидаемый тип или linkname", fm.Path))
	}
	if dest == nil {
		return nil
	}
	if err := dest.MkdirAll(path.Dir(fm.Path), 0o755); err != nil {
		return fmt.Errorf("tapeformat: каталог для %q: %w", fm.Path, err)
	}
	if err := dest.Symlink(fm.Linkname, fm.Path); err != nil {
		return fmt.Errorf("tapeformat: symlink %q: %w", fm.Path, err)
	}
	return nil
}

func extractHardlink(hdr *tar.Header, fm *domain.FileMeta, dest port.FileWriter) error {
	if !fm.IsHardlink() || hdr.Linkname != fm.Linkname || hdr.Size != 0 {
		return damage(fmt.Errorf("tapeformat: запись %q: неожидаемый тип или linkname", fm.Path))
	}
	if dest == nil {
		return nil
	}
	if err := dest.MkdirAll(path.Dir(fm.Path), 0o755); err != nil {
		return fmt.Errorf("tapeformat: каталог для %q: %w", fm.Path, err)
	}
	if err := dest.Link(fm.Linkname, fm.Path); err != nil {
		return fmt.Errorf("tapeformat: hardlink %q: %w", fm.Path, err)
	}
	return nil
}

// extractFile читает содержимое файла из tar, пишет его в dest (если задан)
// и сверяет xxhash с индексом. Сбой записи или хеша удаляет частичный
// файл с dest: недописанный обрывок выглядел бы как успешная часть
// восстановления.
func extractFile(
	ctx context.Context,
	tr *tar.Reader,
	hdr *tar.Header,
	fm *domain.FileMeta,
	dest port.FileWriter,
	buf []byte,
	prog port.ProgressReporter,
	processed *int64,
	total int64,
) error {
	if hdr.Size != fm.Size {
		return damage(fmt.Errorf(
			"tapeformat: файл %q: размер в tar %d, в индексе %d",
			fm.Path, hdr.Size, fm.Size))
	}
	digest := xxhash.New()
	var out io.WriteCloser
	w := io.Writer(digest)
	if dest != nil {
		if err := dest.MkdirAll(path.Dir(fm.Path), 0o755); err != nil {
			return fmt.Errorf("tapeformat: каталог для %q: %w", fm.Path, err)
		}
		f, err := dest.Create(fm.Path)
		if err != nil {
			return fmt.Errorf("tapeformat: создание %q: %w", fm.Path, err)
		}
		out = f
		w = io.MultiWriter(f, digest)
	}
	copyErr := copyFromReader(ctx, tr, w, buf, prog, processed, total, fm.Path)
	if out != nil {
		if cerr := out.Close(); cerr != nil && copyErr == nil {
			copyErr = fmt.Errorf("tapeformat: закрытие %q: %w", fm.Path, cerr)
		}
	}
	if copyErr != nil {
		return discardPartial(dest, fm.Path, copyErr)
	}
	if got := hashHex(digest.Sum64()); got != fm.Hash {
		return discardPartial(dest, fm.Path, damage(fmt.Errorf(
			"tapeformat: файл %q повреждён: хеш индекса %q, фактический %s",
			fm.Path, fm.Hash, got)))
	}
	return nil
}

// discardPartial удаляет частично восстановленный файл, сохраняя
// исходную ошибку; сбой удаления присоединяется (на диске мог остаться
// обрывок — оператор должен об этом знать).
func discardPartial(dest port.FileWriter, path string, cause error) error {
	if dest == nil {
		return cause
	}
	if rmErr := dest.Remove(path); rmErr != nil {
		return errors.Join(cause, fmt.Errorf("tapeformat: удаление частичного файла %q: %w", path, rmErr))
	}
	return cause
}

// drain дочитывает сегмент tar до filemark'а, чтобы лента встала в начало
// следующей сессии.
func drain(r *tapeBlockReader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return fmt.Errorf("tapeformat: дочитывание сессии: %w", err)
	}
	return nil
}
