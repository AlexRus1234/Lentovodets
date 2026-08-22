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

package tapeformat_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/cespare/xxhash/v2"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// Фикстура для golden- и round-trip-тестов: детерминированные файлы,
// времена и хеши.

const (
	fixModTimeDir   int64 = 1691000000_000000000
	fixModTimeMovie int64 = 1691000100_000000000
	fixModTimeNotes int64 = 1691000200_000000000
)

const (
	fixMovieContent  = "hello tape\n"
	fixNotesContent  = "lentovodec golden fixture\n"
	fixMoviePath     = "/tank/media/movie.mkv"
	fixNotesPath     = "/tank/media/notes.txt"
	fixDirPath       = "/tank/media"
	fixTombstonePath = "/tank/media/stale.tmp"
)

// buildFixture возвращает исходную ФС и индекс сессии для неё.
func buildFixture(t *testing.T) (*testutil.MapFS, tapeformat.SessionIndex) {
	t.Helper()
	fs := testutil.NewMapFS(map[string]string{
		fixMoviePath: fixMovieContent,
		fixNotesPath: fixNotesContent,
	})
	if err := fs.MkdirAll(fixDirPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	idx := tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    3,
		Type:          domain.SessionInc,
		JobRunID:      "0f4c2d6a-1111-4222-8333-444455556666",
		Timestamp:     1691928896,
		JobName:       "media",
		Files: []domain.FileMeta{
			{Path: fixDirPath, ModTime: fixModTimeDir, IsDir: true, State: domain.StateAdded},
			{Path: fixMoviePath, Size: int64(len(fixMovieContent)), ModTime: fixModTimeMovie,
				Hash: hashOf(fixMovieContent), State: domain.StateAdded},
			{Path: fixNotesPath, Size: int64(len(fixNotesContent)), ModTime: fixModTimeNotes,
				Hash: hashOf(fixNotesContent), State: domain.StateModified},
			{Path: fixTombstonePath, IsDir: false, State: domain.StateDeleted},
		},
	}
	return fs, idx
}

// singleFileFixture — минимальная сессия из одного файла с заданным
// содержимым (для табличных тестов ошибок).
func singleFileFixture(path, content string) (map[string]string, tapeformat.SessionIndex) {
	files := map[string]string{path: content}
	idx := tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    1,
		Type:          domain.SessionFull,
		JobRunID:      "run-1",
		Timestamp:     1700000000,
		JobName:       "job",
		Files: []domain.FileMeta{{
			Path:    path,
			Size:    int64(len(content)),
			ModTime: 1700000000_000000001,
			Hash:    hashOf(content),
			State:   domain.StateAdded,
		}},
	}
	return files, idx
}

// hashOf — канонический xxhash64 файла в hex (как в индексе).
func hashOf(content string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(content))
}

// writeFixtureSession пишет сессию на ленту без прогресса.
func writeFixtureSession(t *testing.T, tape port.Tape, idx tapeformat.SessionIndex, fs port.FileReader) {
	t.Helper()
	if err := tapeformat.WriteSession(context.Background(), tape, idx, fs, nil); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
}

// hookTape делегирует всё настоящей ленте, но перед выбранными операциями
// может ломаться (замыкание по номеру вызова, счёт с 1).
type hookTape struct {
	port.Tape
	onRead  func(call int) error
	onWrite func(call int) error
	onEOF   func(call int) error
	reads   int
	writes  int
	eofs    int
}

func (h *hookTape) ReadBlock(ctx context.Context) ([]byte, error) {
	h.reads++
	if h.onRead != nil {
		if err := h.onRead(h.reads); err != nil {
			return nil, err
		}
	}
	return h.Tape.ReadBlock(ctx)
}

func (h *hookTape) WriteBlock(ctx context.Context, block []byte) error {
	h.writes++
	if h.onWrite != nil {
		if err := h.onWrite(h.writes); err != nil {
			return err
		}
	}
	return h.Tape.WriteBlock(ctx, block)
}

func (h *hookTape) WriteEOF(ctx context.Context) error {
	h.eofs++
	if h.onEOF != nil {
		if err := h.onEOF(h.eofs); err != nil {
			return err
		}
	}
	return h.Tape.WriteEOF(ctx)
}

// hookFS делегирует всё MapFS, позволяя ломать отдельные операции.
type hookFS struct {
	port.Filesystem
	onStat    func(path string) error
	onOpen    func(path string) (io.ReadCloser, error)
	onMkdir   func(path string) error
	onCreate  func(path string) (io.WriteCloser, error)
	onSymlink func(path string) error
	onLink    func(path string) error
	onRemove  func(path string) error
}

func (h *hookFS) Stat(path string) (port.Entry, error) {
	if h.onStat != nil {
		if err := h.onStat(path); err != nil {
			return nil, err
		}
	}
	return h.Filesystem.Stat(path)
}

func (h *hookFS) Open(path string) (io.ReadCloser, error) {
	if h.onOpen != nil {
		return h.onOpen(path)
	}
	return h.Filesystem.Open(path)
}

func (h *hookFS) MkdirAll(path string, perm os.FileMode) error {
	if h.onMkdir != nil {
		if err := h.onMkdir(path); err != nil {
			return err
		}
	}
	return h.Filesystem.MkdirAll(path, perm)
}

func (h *hookFS) Create(path string) (io.WriteCloser, error) {
	if h.onCreate != nil {
		return h.onCreate(path)
	}
	return h.Filesystem.Create(path)
}

func (h *hookFS) Symlink(linkname, path string) error {
	if h.onSymlink != nil {
		if err := h.onSymlink(path); err != nil {
			return err
		}
	}
	return h.Filesystem.Symlink(linkname, path)
}

func (h *hookFS) Link(oldname, newname string) error {
	if h.onLink != nil {
		if err := h.onLink(newname); err != nil {
			return err
		}
	}
	return h.Filesystem.Link(oldname, newname)
}

func (h *hookFS) Remove(path string) error {
	if h.onRemove != nil {
		if err := h.onRemove(path); err != nil {
			return err
		}
	}
	return h.Filesystem.Remove(path)
}

// hookReader оборачивает io.ReadCloser, ломая Read/Close по крючкам.
type hookReader struct {
	io.ReadCloser
	onRead  func() error
	onClose func() error
}

func (r *hookReader) Read(p []byte) (int, error) {
	if r.onRead != nil {
		if err := r.onRead(); err != nil {
			return 0, err
		}
	}
	return r.ReadCloser.Read(p)
}

func (r *hookReader) Close() error {
	if r.onClose != nil {
		if err := r.onClose(); err != nil {
			return err
		}
	}
	return r.ReadCloser.Close()
}

// hookWriter оборачивает io.WriteCloser, ломая Write/Close по крючкам.
type hookWriter struct {
	io.WriteCloser
	onWrite func() error
	onClose func() error
}

func (w *hookWriter) Write(p []byte) (int, error) {
	if w.onWrite != nil {
		if err := w.onWrite(); err != nil {
			return 0, err
		}
	}
	return w.WriteCloser.Write(p)
}

func (w *hookWriter) Close() error {
	if w.onClose != nil {
		if err := w.onClose(); err != nil {
			return err
		}
	}
	return w.WriteCloser.Close()
}

// cancelReader отдаёт один кусок данных, отменяя контекст, затем EOF —
// детерминированная отмена в середине копирования файла.
type cancelReader struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "data"), nil
}

func (r *cancelReader) Close() error { return nil }

// recProg запоминает все обновления прогресса.
type recProg struct {
	updates []port.ProgressUpdate
	done    bool
	failed  error
}

func (p *recProg) Update(u port.ProgressUpdate) { p.updates = append(p.updates, u) }
func (p *recProg) Done()                        { p.done = true }
func (p *recProg) Fail(err error)               { p.failed = err }
