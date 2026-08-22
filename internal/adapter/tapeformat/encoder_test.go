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
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

func TestWriteSession_Structure(t *testing.T) {
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	prog := &recProg{}

	if err := tapeformat.WriteSession(context.Background(), tape, idx, fs, prog); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	// docs/FORMAT.md §4: индекс (1 блок) + filemark, tar (1 блок) + filemark.
	if got := tape.BlockCount(); got != 2 {
		t.Errorf("BlockCount = %d, want 2", got)
	}
	_, marks := tape.Snapshot()
	if !reflect.DeepEqual(marks, []int{1, 2}) {
		t.Errorf("marks = %v, want [1 2]", marks)
	}

	last := prog.updates[len(prog.updates)-1]
	if last.CurrentFile != fixNotesPath {
		t.Errorf("последний файл прогресса = %q, want %q", last.CurrentFile, fixNotesPath)
	}
	if last.ProcessedBytes != last.TotalBytes {
		t.Errorf("processed = %d, total = %d (прогресс не сошёлся)", last.ProcessedBytes, last.TotalBytes)
	}
	if last.Phase != port.PhaseWrite {
		t.Errorf("phase = %q, want write", last.Phase)
	}
	blocks, _ := tape.Snapshot()
	tr := tar.NewReader(bytes.NewReader(blocks[1]))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("чтение заголовка каталога: %v", err)
	}
	if hdr.Typeflag != tar.TypeDir || hdr.Mode != 0o755 {
		t.Fatalf("режим каталога = %#o, want 0755", hdr.Mode)
	}
}

func TestWriteSession_EmptySession(t *testing.T) {
	tape := testutil.NewFakeTape()
	idx := tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    1,
		Type:          domain.SessionFull,
		JobRunID:      "r", Timestamp: 1, JobName: "j",
	}
	if err := tapeformat.WriteSession(context.Background(), tape, idx, testutil.NewMapFS(nil), nil); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if tape.BlockCount() != 2 {
		t.Errorf("BlockCount = %d, want 2 (индекс + пустой tar)", tape.BlockCount())
	}
}

func TestWriteSession_MultiBlockIndex(t *testing.T) {
	// ~3000 записей → JSON длиннее BlockSize → индекс в 2 блоках.
	filesMap := map[string]string{}
	files := make([]domain.FileMeta, 0, 3000)
	for i := 0; i < 3000; i++ {
		path := fmt.Sprintf("/bulk/file-%04d.bin", i)
		filesMap[path] = "c"
		files = append(files, domain.FileMeta{
			Path: path, Size: 1, ModTime: int64(i), Hash: hashOf("c"), State: domain.StateAdded,
		})
	}
	fs := testutil.NewMapFS(filesMap)
	idx := tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion, SessionNum: 1, Type: domain.SessionFull,
		JobRunID: "r", Timestamp: 1, JobName: "bulk", Files: files,
	}
	tape := testutil.NewFakeTape()
	if err := tapeformat.WriteSession(context.Background(), tape, idx, fs, nil); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	_, marks := tape.Snapshot()
	if marks[0] != 2 {
		t.Fatalf("filemark после индекса на блоке %d, want 2 (индекс из 2 блоков)", marks[0])
	}

	if err := tape.Rewind(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := tapeformat.ReadSession(context.Background(), tape, testutil.NewMapFS(nil), nil, nil)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if !reflect.DeepEqual(got, files) {
		t.Errorf("round-trip многофайловой сессии: прочитано %d файлов", len(got))
	}
}

func TestWriteSession_Errors(t *testing.T) {
	boom := errors.New("boom")
	type setup func() (ctx context.Context, tape port.Tape, idx tapeformat.SessionIndex, fs port.FileReader)

	tests := []struct {
		name    string
		setup   setup
		wantMsg string
	}{
		{
			name: "недопустимый тип сессии",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				_, idx := singleFileFixture("/f.txt", "data")
				idx.Type = "WHAT"
				return context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(nil)
			},
			wantMsg: "тип сессии",
		},
		{
			name: "недопустимое состояние файла",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				_, idx := singleFileFixture("/f.txt", "data")
				idx.Files[0].State = "X"
				return context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(nil)
			},
			wantMsg: "состояние",
		},
		{
			name: "контекст уже отменён",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, testutil.NewFakeTape(), idx, testutil.NewMapFS(files)
			},
			wantMsg: "context canceled",
		},
		{
			name: "ошибка записи блока индекса",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				ht := &hookTape{Tape: testutil.NewFakeTape(), onWrite: func(c int) error {
					if c == 1 {
						return boom
					}
					return nil
				}}
				return context.Background(), ht, idx, testutil.NewMapFS(files)
			},
			wantMsg: "запись индекса",
		},
		{
			name: "ошибка filemark после индекса",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				ht := &hookTape{Tape: testutil.NewFakeTape(), onEOF: func(c int) error {
					if c == 1 {
						return boom
					}
					return nil
				}}
				return context.Background(), ht, idx, testutil.NewMapFS(files)
			},
			wantMsg: "filemark после индекса",
		},
		{
			name: "ошибка stat",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				hf := &hookFS{Filesystem: testutil.NewMapFS(files), onStat: func(string) error { return boom }}
				return context.Background(), testutil.NewFakeTape(), idx, hf
			},
			wantMsg: "stat",
		},
		{
			name: "невалидный tar-заголовок (отрицательный размер)",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				idx.Files[0].Size = -1
				return context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(files)
			},
			wantMsg: "заголовок tar",
		},
		{
			name: "ошибка открытия файла",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				hf := &hookFS{Filesystem: testutil.NewMapFS(files), onOpen: func(string) (io.ReadCloser, error) {
					return nil, boom
				}}
				return context.Background(), testutil.NewFakeTape(), idx, hf
			},
			wantMsg: "открытие",
		},
		{
			name: "ошибка чтения файла",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				hf := &hookFS{Filesystem: testutil.NewMapFS(files), onOpen: func(string) (io.ReadCloser, error) {
					inner, err := testutil.NewMapFS(files).Open("/f.txt")
					if err != nil {
						return nil, err
					}
					return &hookReader{ReadCloser: inner, onRead: func() error { return boom }}, nil
				}}
				return context.Background(), testutil.NewFakeTape(), idx, hf
			},
			wantMsg: "чтение",
		},
		{
			name: "файл вырос — tar write too long",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				idx.Files[0].Size = 3 // в ФС 4 байта
				return context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(files)
			},
			wantMsg: "запись",
		},
		{
			name: "ошибка закрытия исходного файла",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				hf := &hookFS{Filesystem: testutil.NewMapFS(files), onOpen: func(string) (io.ReadCloser, error) {
					inner, err := testutil.NewMapFS(files).Open("/f.txt")
					if err != nil {
						return nil, err
					}
					return &hookReader{ReadCloser: inner, onClose: func() error { return boom }}, nil
				}}
				return context.Background(), testutil.NewFakeTape(), idx, hf
			},
			wantMsg: "закрытие",
		},
		{
			name: "файл изменился — хеш не сошёлся",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				idx.Files[0].Hash = "0000000000000000"
				return context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(files)
			},
			wantMsg: "изменился",
		},
		{
			name: "файл усох — tar missed bytes",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				idx.Files[0].Size = 100 // в ФС 4 байта
				return context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(files)
			},
			wantMsg: "закрытие tar",
		},
		{
			name: "ошибка записи блока tar (flush)",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				ht := &hookTape{Tape: testutil.NewFakeTape(), onWrite: func(c int) error {
					if c == 2 {
						return boom
					}
					return nil
				}}
				return context.Background(), ht, idx, testutil.NewMapFS(files)
			},
			wantMsg: "запись блока",
		},
		{
			name: "ошибка filemark после tar",
			setup: func() (context.Context, port.Tape, tapeformat.SessionIndex, port.FileReader) {
				files, idx := singleFileFixture("/f.txt", "data")
				ht := &hookTape{Tape: testutil.NewFakeTape(), onEOF: func(c int) error {
					if c == 2 {
						return boom
					}
					return nil
				}}
				return context.Background(), ht, idx, testutil.NewMapFS(files)
			},
			wantMsg: "filemark после tar",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, tape, idx, fs := tt.setup()
			err := tapeformat.WriteSession(ctx, tape, idx, fs, nil)
			if err == nil {
				t.Fatal("err = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %v, want вхождение %q", err, tt.wantMsg)
			}
		})
	}
}

func TestWriteSession_CancelDuringCopy(t *testing.T) {
	files, idx := singleFileFixture("/f.txt", "data")
	ctx, cancel := context.WithCancel(context.Background())
	hf := &hookFS{Filesystem: testutil.NewMapFS(files), onOpen: func(string) (io.ReadCloser, error) {
		return &cancelReader{cancel: cancel}, nil
	}}

	err := tapeformat.WriteSession(ctx, testutil.NewFakeTape(), idx, hf, nil)
	if err == nil {
		t.Fatal("err = nil, want context canceled")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("err = %v, want context canceled", err)
	}
}

func TestWriteSession_CancelBetweenFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fs := testutil.NewMapFS(map[string]string{"/a": "a", "/b": "b"})
	idx := tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion, SessionNum: 1, Type: domain.SessionFull,
		JobRunID: "r", Timestamp: 1, JobName: "j",
		Files: []domain.FileMeta{
			{Path: "/a", Size: 1, Hash: hashOf("a"), State: domain.StateAdded},
			{Path: "/b", Size: 1, Hash: hashOf("b"), State: domain.StateAdded},
		},
	}
	// Файл /a копируется целиком (два Read: данные, затем EOF), и второй
	// Read отменяет контекст: copy завершится чисто, а следующий виток
	// цикла writeTar увидит отмену.
	var reads int
	hf := &hookFS{Filesystem: fs, onOpen: func(p string) (io.ReadCloser, error) {
		inner, err := fs.Open(p)
		if err != nil {
			return nil, err
		}
		if p == "/a" {
			return &hookReader{ReadCloser: inner, onRead: func() error {
				reads++
				if reads >= 2 {
					cancel()
				}
				return nil
			}}, nil
		}
		return inner, nil
	}}

	err := tapeformat.WriteSession(ctx, testutil.NewFakeTape(), idx, hf, nil)
	if err == nil || !strings.Contains(err.Error(), "запись tar") {
		t.Fatalf("err = %v, want «запись tar: … context canceled»", err)
	}
}
