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

// Запись сессии на ленту: JSON-индекс + tar-поток.
// См. docs/FORMAT.md §4 (раскладка), §6 (индекс), §7 (tar).

package tapeformat

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/cespare/xxhash/v2"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// SessionIndex — JSON-индекс сессии; канон полей — docs/FORMAT.md §6.
type SessionIndex struct {
	FormatVersion int                `json:"format_version"`
	SessionNum    int32              `json:"session_num"`
	Type          domain.SessionType `json:"type"`
	JobRunID      string             `json:"job_run_id"`
	Timestamp     int64              `json:"timestamp"`
	JobName       string             `json:"job_name"`
	Files         []domain.FileMeta  `json:"files"`
}

// WriteSession пишет одну сессию в текущую позицию ленты:
//
//	блоки INDEX … MTWEOF, блоки TAR … MTWEOF
//
// Ярлык кассеты и позиционирование (MTREW/MTFSF/MTEOM) — зона
// ответственности вызывающего (docs/FORMAT.md §4, §9).
//
// Содержимое файлов читается из fs; каждый файл проходит через xxhash64,
// вычисленный хеш сверяется с FileMeta.Hash — файл, изменившийся между
// сканированием и записью, считается ошибкой.
//
// Прогресс: только Update с фазой port.PhaseWrite; Done/Fail публикует
// вызывающий. prog == nil допустим.
func WriteSession(
	ctx context.Context,
	tape port.Tape,
	idx SessionIndex,
	fs port.FileReader,
	prog port.ProgressReporter,
) error {
	prog = progressOr(prog)
	if err := validateIndex(&idx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tapeformat: запись сессии: %w", err)
	}
	if err := writeIndex(ctx, tape, idx); err != nil {
		return err
	}
	if err := writeTar(ctx, tape, idx.Files, fs, prog); err != nil {
		return err
	}
	if err := tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("tapeformat: filemark после tar: %w", err)
	}
	return nil
}

// validateIndex проверяет индекс и нормализует пути на месте;
// nil Files заменяется пустым списком (JSON "[]", а не null).
func validateIndex(idx *SessionIndex) error {
	if !idx.Type.Valid() {
		return fmt.Errorf("tapeformat: недопустимый тип сессии %q", idx.Type)
	}
	for i := range idx.Files {
		if !idx.Files[i].State.Valid() {
			return fmt.Errorf(
				"tapeformat: файл %q: недопустимое состояние %q",
				idx.Files[i].Path, idx.Files[i].State)
		}
		idx.Files[i].Path = domain.NormalizePath(idx.Files[i].Path)
	}
	if idx.Files == nil {
		idx.Files = []domain.FileMeta{}
	}
	return nil
}

// writeIndex кодирует индекс (JSON, добитый нулями до кратного BlockSize)
// и пишет его блоками, завершая filemark'ом.
func writeIndex(ctx context.Context, tape port.Tape, idx SessionIndex) error {
	padded, err := encodeBlocks(idx, 0)
	if err == nil {
		err = writeIndexBlocks(ctx, tape, padded)
	}
	return err
}

// writeIndexBlocks пишет закодированный индекс блоками и ставит filemark.
func writeIndexBlocks(ctx context.Context, tape port.Tape, padded []byte) error {
	for off := 0; off < len(padded); off += domain.BlockSize {
		if err := tape.WriteBlock(ctx, padded[off:off+domain.BlockSize]); err != nil {
			return fmt.Errorf("tapeformat: запись индекса: %w", err)
		}
	}
	if err := tape.WriteEOF(ctx); err != nil {
		return fmt.Errorf("tapeformat: filemark после индекса: %w", err)
	}
	return nil
}

// writeTar пишет tar-поток (GNU) с файлами сессии, кроме tombstone'ов,
// и добивает его до полной блочности.
func writeTar(ctx context.Context, tape port.Tape, files []domain.FileMeta, fs port.FileReader, prog port.ProgressReporter) error {
	var total int64
	for i := range files {
		if !files[i].IsDeleted() {
			total += files[i].Size
		}
	}
	var processed int64
	buf := make([]byte, domain.CopyBuffer)
	bw := &tapeBlockWriter{ctx: ctx, tape: tape}
	tw := tar.NewWriter(bw)
	for i := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("tapeformat: запись tar: %w", err)
		}
		if files[i].IsDeleted() {
			continue // tombstone'ы в tar не попадают (docs/FORMAT.md §7)
		}
		prog.Update(port.ProgressUpdate{
			Phase:          port.PhaseWrite,
			CurrentFile:    files[i].Path,
			ProcessedBytes: processed,
			TotalBytes:     total,
		})
		if err := writeTarFile(ctx, tw, &files[i], fs, buf, prog, &processed, total); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tapeformat: закрытие tar: %w", err)
	}
	if err := bw.flush(); err != nil {
		return err
	}
	return nil
}

// writeTarFile пишет в tar один файл (заголовок + содержимое).
func writeTarFile(
	ctx context.Context,
	tw *tar.Writer,
	fm *domain.FileMeta,
	fs port.FileReader,
	buf []byte,
	prog port.ProgressReporter,
	processed *int64,
	total int64,
) error {
	info, err := fs.Stat(fm.Path)
	if err != nil {
		return fmt.Errorf("tapeformat: stat %q: %w", fm.Path, err)
	}
	hdr := &tar.Header{
		Format:   tar.FormatGNU,
		Name:     fm.Path,
		Typeflag: tar.TypeReg,
		Size:     fm.Size,
		Mode:     int64(info.Mode().Perm()),
		ModTime:  time.Unix(fm.ModTime/1e9, 0), // наносекунды — только в индексе
	}
	if fm.IsDir {
		hdr.Typeflag = tar.TypeDir
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tapeformat: заголовок tar %q: %w", fm.Path, err)
	}
	if fm.IsDir {
		return nil
	}
	return copyFileToTar(ctx, tw, fm, fs, buf, prog, processed, total)
}

// copyFileToTar читает файл из fs в tar, попутно считая xxhash64,
// и сверяет результат с FileMeta.Hash.
func copyFileToTar(
	ctx context.Context,
	tw *tar.Writer,
	fm *domain.FileMeta,
	fs port.FileReader,
	buf []byte,
	prog port.ProgressReporter,
	processed *int64,
	total int64,
) error {
	src, err := fs.Open(fm.Path)
	if err != nil {
		return fmt.Errorf("tapeformat: открытие %q: %w", fm.Path, err)
	}
	digest := xxhash.New()
	copyErr := copyFromReader(ctx, src, io.MultiWriter(tw, digest), buf, prog, processed, total, fm.Path)
	closeErr := src.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return fmt.Errorf("tapeformat: закрытие %q: %w", fm.Path, closeErr)
	}
	if got := hashHex(digest.Sum64()); got != fm.Hash {
		return fmt.Errorf(
			"tapeformat: файл %q изменился между сканом и записью: хеш индекса %q, фактический %s",
			fm.Path, fm.Hash, got)
	}
	return nil
}

// hashHex — каноническое представление xxhash64: 16 hex-символов,
// lowercase (docs/FORMAT.md §2).
func hashHex(sum uint64) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = digits[sum&0xf]
		sum >>= 4
	}
	return string(out)
}
