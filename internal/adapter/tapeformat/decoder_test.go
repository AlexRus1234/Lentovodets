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
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

func TestReadSession_RoundTrip(t *testing.T) {
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs)
	if err := tape.Rewind(context.Background()); err != nil {
		t.Fatal(err)
	}

	dest := testutil.NewMapFS(nil)
	prog := &recProg{}
	files, err := tapeformat.ReadSession(context.Background(), tape, dest, nil, prog)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if !reflect.DeepEqual(files, idx.Files) {
		t.Errorf("файлы индекса не совпали:\n got %+v\nwant %+v", files, idx.Files)
	}

	// Содержимое восстановлено байт-в-байт.
	for path, want := range map[string]string{fixMoviePath: fixMovieContent, fixNotesPath: fixNotesContent} {
		got, err := readAllFrom(dest, path)
		if err != nil {
			t.Fatalf("чтение %q из dest: %v", path, err)
		}
		if got != want {
			t.Errorf("%q: %q, want %q", path, got, want)
		}
	}
	info, err := dest.Stat(fixDirPath)
	if err != nil || !info.IsDir() {
		t.Errorf("каталог %q не восстановлен: %v", fixDirPath, err)
	}
	if _, err := dest.Stat(fixTombstonePath); err == nil {
		t.Error("tombstone не должен попасть в dest")
	}
	if len(prog.updates) == 0 {
		t.Error("прогресс не публиковался")
	}
}

func TestReadSession_SpecialFilesRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := testutil.NewMapFS(map[string]string{"/data/first": "payload"})
	src.AddSymlink("/data/dangling", "missing-target")
	idx := tapeformat.SessionIndex{FormatVersion: domain.FormatVersion, SessionNum: 1, Type: domain.SessionFull, JobRunID: "run", Timestamp: 1, JobName: "j", Files: []domain.FileMeta{
		{Path: "/data/first", Size: 7, Hash: hashOf("payload"), State: domain.StateAdded},
		{Path: "/data/second", Type: domain.FileTypeHardlink, Linkname: "/data/first", State: domain.StateAdded},
		{Path: "/data/dangling", Type: domain.FileTypeSymlink, Linkname: "missing-target", Size: 14, State: domain.StateAdded},
	}}
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, src)
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	dest := testutil.NewMapFS(nil)
	prog := &recProg{}
	got, err := tapeformat.ReadSession(ctx, tape, dest, nil, prog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, idx.Files) {
		t.Fatalf("index = %+v, want %+v", got, idx.Files)
	}
	if link, err := dest.Readlink("/data/dangling"); err != nil || link != "missing-target" {
		t.Fatalf("restored symlink = %q, %v", link, err)
	}
	first, err := dest.Stat("/data/first")
	if err != nil || first.IsDir() {
		t.Fatalf("restored first: %v", err)
	}
	second, err := dest.Stat("/data/second")
	if err != nil || second.IsDir() {
		t.Fatalf("restored second: %v", err)
	}
	if len(prog.updates) == 0 || prog.updates[len(prog.updates)-1].TotalBytes != 7 {
		t.Fatalf("special-file progress total = %d, want 7", prog.updates[len(prog.updates)-1].TotalBytes)
	}
}

func TestReadSession_OldIndexWithoutTypeIsRegular(t *testing.T) {
	ctx := context.Background()
	oldIndex := []byte(`{"format_version":2,"session_num":1,"type":"FULL","job_run_id":"run","timestamp":1,"job_name":"j","files":[{"path":"/f","size":3,"mod_time":1,"is_dir":false,"hash":"` + hashOf("abc") + `","state":"A"}]}`)
	tape := craftSessionTape(t, oldIndex, craftTar(t, tarEntry{name: "/f", size: 3, content: "abc"}))
	files, err := tapeformat.ReadSession(ctx, tape, testutil.NewMapFS(nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Type != "" || files[0].IsSymlink() || files[0].IsHardlink() {
		t.Fatalf("old file metadata = %+v, want regular semantics", files)
	}
}

func TestReadSession_VerifyOnly(t *testing.T) {
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs)
	if err := tape.Rewind(context.Background()); err != nil {
		t.Fatal(err)
	}

	files, err := tapeformat.ReadSession(context.Background(), tape, nil, nil, nil)
	if err != nil {
		t.Fatalf("ReadSession (dest=nil): %v", err)
	}
	if !reflect.DeepEqual(files, idx.Files) {
		t.Errorf("файлы индекса не совпали: %+v", files)
	}
}

// TestReadSession_SelectiveFilter — include != nil: на ФС попадают
// только включённые пути (и компаньоны хардлинков), но хеши
// остальных записей всё равно сверяются.
func TestReadSession_SelectiveFilter(t *testing.T) {
	t.Run("только включённый файл", func(t *testing.T) {
		fs, idx := buildFixture(t)
		tape := testutil.NewFakeTape()
		writeFixtureSession(t, tape, idx, fs)
		if err := tape.Rewind(context.Background()); err != nil {
			t.Fatal(err)
		}
		dest := testutil.NewMapFS(nil)
		files, err := tapeformat.ReadSession(context.Background(), tape, dest,
			func(p string) bool { return p == fixNotesPath }, nil)
		if err != nil {
			t.Fatalf("ReadSession: %v", err)
		}
		if !reflect.DeepEqual(files, idx.Files) {
			t.Errorf("include не должен менять возвращаемый индекс: %+v", files)
		}
		if got, err := readAllFrom(dest, fixNotesPath); err != nil || got != fixNotesContent {
			t.Errorf("включённый файл: %q, %v", got, err)
		}
		if _, err := dest.Stat(fixMoviePath); err == nil {
			t.Error("невключённый файл не должен попасть в dest")
		}
	})

	t.Run("хардлинк пишет компаньона", func(t *testing.T) {
		ctx := context.Background()
		src := testutil.NewMapFS(map[string]string{"/data/first": "payload"})
		idx := tapeformat.SessionIndex{FormatVersion: domain.FormatVersion, SessionNum: 1, Type: domain.SessionFull, JobRunID: "run", Timestamp: 1, JobName: "j", Files: []domain.FileMeta{
			{Path: "/data/first", Size: 7, Hash: hashOf("payload"), State: domain.StateAdded},
			{Path: "/data/second", Type: domain.FileTypeHardlink, Linkname: "/data/first", State: domain.StateAdded},
		}}
		tape := testutil.NewFakeTape()
		writeFixtureSession(t, tape, idx, src)
		if err := tape.Rewind(ctx); err != nil {
			t.Fatal(err)
		}
		dest := testutil.NewMapFS(nil)
		if _, err := tapeformat.ReadSession(ctx, tape, dest,
			func(p string) bool { return p == "/data/second" }, nil); err != nil {
			t.Fatalf("ReadSession: %v", err)
		}
		if _, err := dest.Stat("/data/first"); err != nil {
			t.Fatalf("компаньон хардлинка не восстановлен: %v", err)
		}
		if _, err := dest.Stat("/data/second"); err != nil {
			t.Fatalf("сам хардлинк не восстановлен: %v", err)
		}
	})

	t.Run("хеш невключённого файла всё равно проверяется", func(t *testing.T) {
		idx := craftBaseIndex()
		idx.Files = append(idx.Files, domain.FileMeta{
			Path: "/broken.txt", Size: 3, ModTime: 1,
			Hash: "deadbeefdeadbeef", State: domain.StateAdded,
		})
		tape := craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t,
			tarEntry{name: "/f.txt", size: 3, content: "abc"},
			tarEntry{name: "/broken.txt", size: 3, content: "abc"},
		))
		_, err := tapeformat.ReadSession(context.Background(), tape, testutil.NewMapFS(nil),
			func(p string) bool { return p == "/f.txt" }, nil)
		if err == nil || !strings.Contains(err.Error(), "повреждён") {
			t.Fatalf("хеш невключённого файла не проверен: %v", err)
		}
	})

	t.Run("фильтр по корню захватывает поддерево", func(t *testing.T) {
		ctx := context.Background()
		src := testutil.NewMapFS(map[string]string{"/keep/a": "a", "/drop/b": "b"})
		idx := tapeformat.SessionIndex{FormatVersion: domain.FormatVersion, SessionNum: 1, Type: domain.SessionFull, JobRunID: "run", Timestamp: 1, JobName: "j", Files: []domain.FileMeta{
			{Path: "/keep/a", Size: 1, ModTime: 1, Hash: hashOf("a"), State: domain.StateAdded},
			{Path: "/drop/b", Size: 1, ModTime: 1, Hash: hashOf("b"), State: domain.StateAdded},
		}}
		tape := testutil.NewFakeTape()
		writeFixtureSession(t, tape, idx, src)
		if err := tape.Rewind(ctx); err != nil {
			t.Fatal(err)
		}
		dest := testutil.NewMapFS(nil)
		if _, err := tapeformat.ReadSession(ctx, tape, dest,
			func(p string) bool { return strings.HasPrefix(p, "/keep/") }, nil); err != nil {
			t.Fatalf("ReadSession: %v", err)
		}
		if _, err := dest.Stat("/keep/a"); err != nil {
			t.Fatalf("файл под корнем не восстановлен: %v", err)
		}
		if _, err := dest.Stat("/drop/b"); err == nil {
			t.Error("файл вне корня не должен попадать в dest")
		}
	})
}

// TestReadSession_CorruptFileRemovedFromDest — несовпавший хеш:
// частичный файл удаляется из dest, ошибка сохраняется (обрывок
// выглядел бы как успешная часть восстановления).
func TestReadSession_CorruptFileRemovedFromDest(t *testing.T) {
	idx := craftBaseIndex()
	idx.Files[0].Hash = "deadbeefdeadbeef"
	tape := craftSessionTape(t, craftIndexBlock(t, idx),
		craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
	dest := testutil.NewMapFS(nil)
	_, err := tapeformat.ReadSession(context.Background(), tape, dest, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "повреждён") {
		t.Fatalf("err = %v; want хеш-ошибка", err)
	}
	if _, serr := dest.Stat("/f.txt"); serr == nil {
		t.Error("частичный файл должен удаляться из dest")
	}
}

// TestReadSession_IndexFileMissingFromTar — файл есть в индексе, но в
// tar не пришёл (урезанный в точности по записи tar): ошибка, а не
// молчаливый успех с недостающими файлами.
func TestReadSession_IndexFileMissingFromTar(t *testing.T) {
	idx := craftBaseIndex()
	idx.Files = append(idx.Files, domain.FileMeta{
		Path: "/gone.txt", Size: 3, ModTime: 1, Hash: hashOf("xyz"), State: domain.StateAdded,
	})
	tape := craftSessionTape(t, craftIndexBlock(t, idx),
		craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
	_, err := tapeformat.ReadSession(context.Background(), tape, testutil.NewMapFS(nil), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "отсутствует в tar") {
		t.Fatalf("err = %v; want «отсутствует в tar»", err)
	}
}

func TestReadSession_TwoSessionsSequential(t *testing.T) {
	ctx := context.Background()
	src := testutil.NewMapFS(map[string]string{"/s/a.txt": "alpha", "/s/b.txt": "beta"})
	mkIdx := func(num int32, files ...domain.FileMeta) tapeformat.SessionIndex {
		return tapeformat.SessionIndex{
			FormatVersion: domain.FormatVersion, SessionNum: num,
			Type: domain.SessionInc, JobRunID: "run", Timestamp: 1700000000, JobName: "j",
			Files: files,
		}
	}
	file := func(path, content string, state domain.FileState) domain.FileMeta {
		return domain.FileMeta{
			Path: path, Size: int64(len(content)), ModTime: 1,
			Hash: hashOf(content), State: state,
		}
	}
	s1 := mkIdx(1, file("/s/a.txt", "alpha", domain.StateAdded))
	s2 := mkIdx(2,
		file("/s/b.txt", "beta", domain.StateAdded),
		file("/s/a.txt", "", domain.StateDeleted),
	)

	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, s1, src)
	if err := tape.EndOfData(ctx); err != nil {
		t.Fatal(err)
	}
	writeFixtureSession(t, tape, s2, src)

	// Раскладка без ярлыка: [idx1 M tar1 M idx2 M tar2 M].
	if _, marks := tape.Snapshot(); !reflect.DeepEqual(marks, []int{1, 2, 3, 4}) {
		t.Fatalf("marks = %v, want [1 2 3 4]", marks)
	}

	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	dest := testutil.NewMapFS(nil)
	got1, err := tapeformat.ReadSession(ctx, tape, dest, nil, nil)
	if err != nil {
		t.Fatalf("чтение сессии 1: %v", err)
	}
	if !reflect.DeepEqual(got1, s1.Files) {
		t.Errorf("сессия 1: %+v", got1)
	}
	got2, err := tapeformat.ReadSession(ctx, tape, dest, nil, nil)
	if err != nil {
		t.Fatalf("чтение сессии 2 подряд: %v", err)
	}
	if !reflect.DeepEqual(got2, s2.Files) {
		t.Errorf("сессия 2: %+v", got2)
	}
	if _, err := dest.Stat("/s/a.txt"); err != nil {
		t.Errorf("a.txt из сессии 1 не восстановлен: %v", err)
	}
	if _, err := dest.Stat("/s/b.txt"); err != nil {
		t.Errorf("b.txt из сессии 2 не восстановлен: %v", err)
	}
}

func TestReadSession_PositionedByFilemarks(t *testing.T) {
	ctx := context.Background()
	src := testutil.NewMapFS(map[string]string{"/s/a.txt": "alpha", "/s/b.txt": "beta"})
	_, s1 := singleFileFixture("/s/a.txt", "alpha")
	_, s2 := singleFileFixture("/s/b.txt", "beta")
	s1.SessionNum, s2.SessionNum = 1, 2

	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, s1, src)
	if err := tape.EndOfData(ctx); err != nil {
		t.Fatal(err)
	}
	writeFixtureSession(t, tape, s2, src)

	// К сессии 2 без ярлыка: MTFSF(2) (docs/FORMAT.md §9).
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.ForwardFilemarks(ctx, 2); err != nil {
		t.Fatal(err)
	}
	files, err := tapeformat.ReadSession(ctx, tape, testutil.NewMapFS(nil), nil, nil)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if !reflect.DeepEqual(files, s2.Files) {
		t.Errorf("прочитана не та сессия: %+v", files)
	}
}

// craftIndexBlock маршалит индекс как есть (без паддинга — ленте всё равно).
func craftIndexBlock(t *testing.T, idx tapeformat.SessionIndex) []byte {
	t.Helper()
	b, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal индекса: %v", err)
	}
	return b
}

// tarEntry — запись для ручной сборки tar-сегмента.
type tarEntry struct {
	name     string
	typeflag byte
	link     string
	size     int64
	content  string
}

func craftTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.name, Typeflag: e.typeflag, Linkname: e.link,
			Size: e.size, Mode: 0o644, Format: tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %q: %v", e.name, err)
		}
		if e.content != "" {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatalf("Write %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	return buf.Bytes()
}

// craftSessionTape пишет на ленту сессию из сырых блоков (индекс, tar),
// нарезая tar на блоки domain.BlockSize как это делает кодировщик,
// и перематывает в начало.
func craftSessionTape(t *testing.T, indexBlock []byte, tarBytes []byte) *testutil.FakeTape {
	t.Helper()
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	if err := tape.WriteBlock(ctx, indexBlock); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatal(err)
	}
	for len(tarBytes) > 0 {
		n := domain.BlockSize
		if n > len(tarBytes) {
			n = len(tarBytes)
		}
		block := make([]byte, domain.BlockSize)
		copy(block, tarBytes[:n])
		if err := tape.WriteBlock(ctx, block); err != nil {
			t.Fatal(err)
		}
		tarBytes = tarBytes[n:]
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}
	return tape
}

func craftBaseIndex() tapeformat.SessionIndex {
	return tapeformat.SessionIndex{
		FormatVersion: domain.FormatVersion, SessionNum: 1,
		Type: domain.SessionFull, JobRunID: "r", Timestamp: 1, JobName: "j",
		Files: []domain.FileMeta{{
			Path: "/f.txt", Size: 3, ModTime: 1,
			Hash: hashOf("abc"), State: domain.StateAdded,
		}},
	}
}

func TestReadSession_Errors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		tape    func(t *testing.T) port.Tape
		dest    func() port.FileWriter
		wantErr error
		wantMsg string
	}{
		{
			name: "ошибка чтения блока индекса",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				_, idx := singleFileFixture("/f.txt", "data")
				inner := craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t, tarEntry{name: "/f.txt", size: 4, content: "data"}))
				return &hookTape{Tape: inner, onRead: func(c int) error {
					if c == 1 {
						return boom
					}
					return nil
				}}
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "чтение индекса",
		},
		{
			name: "пустой индекс",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				ctx := context.Background()
				tape := testutil.NewFakeTape()
				_ = tape.WriteEOF(ctx)
				_ = tape.WriteEOF(ctx)
				_ = tape.Rewind(ctx)
				return tape
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "индекс сессии пуст",
		},
		{
			name: "мусор вместо JSON",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				return craftSessionTape(t, []byte("{ not json"), craftTar(t))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "разбор индекса",
		},
		{
			name: "версия индекса новее поддерживаемой",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				idx.FormatVersion = domain.FormatVersion + 1
				return craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantErr: &domain.NewerFormatError{},
		},
		{
			name: "недопустимый тип сессии в индексе",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				idx.Type = "WAT"
				return craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "тип сессии",
		},
		{
			name: "недопустимое состояние файла в индексе",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				idx.Files[0].State = "Z"
				return craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "состояние",
		},
		{
			name: "ошибка чтения блока tar",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				inner := craftSessionTape(t,
					craftIndexBlock(t, craftBaseIndex()),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
				return &hookTape{Tape: inner, onRead: func(c int) error {
					if c == 3 { // 1=индекс, 2=filemark, 3=первый блок tar
						return boom
					}
					return nil
				}}
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "чтение tar",
		},
		{
			name: "запись tar отсутствует в индексе",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				return craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/stranger.txt", size: 3, content: "xyz"}))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "отсутствует в индексе",
		},
		{
			name: "размер в tar не совпал с индексом",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				idx.Files[0].Size = 99
				return craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "размер в tar",
		},
		{
			name: "неожидаемый тип записи tar",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				return craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/f.txt", typeflag: tar.TypeSymlink, link: "/etc"}))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "неожидаемый тип",
		},
		{
			name: "хеш не совпал (режим восстановления)",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				idx.Files[0].Hash = "0000000000000000"
				return craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "повреждён",
		},
		{
			name: "хеш не совпал (режим проверки, dest=nil)",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				idx := craftBaseIndex()
				idx.Files[0].Hash = "0000000000000000"
				return craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
			},
			dest:    nil,
			wantMsg: "повреждён",
		},
		{
			name: "ошибка создания каталога",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				fs, idx := buildFixture(t)
				tape := testutil.NewFakeTape()
				writeFixtureSession(t, tape, idx, fs)
				_ = tape.Rewind(context.Background())
				return tape
			},
			dest: func() port.FileWriter {
				return &hookFS{Filesystem: testutil.NewMapFS(nil), onMkdir: func(string) error { return boom }}
			},
			wantMsg: "создание каталога",
		},
		{
			name: "ошибка создания каталога-родителя для файла",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				return craftSessionTape(t,
					craftIndexBlock(t, craftBaseIndex()),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
			},
			dest: func() port.FileWriter {
				return &hookFS{Filesystem: testutil.NewMapFS(nil), onMkdir: func(string) error { return boom }}
			},
			wantMsg: "каталог для",
		},
		{
			name: "ошибка создания файла",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				inner := craftSessionTape(t,
					craftIndexBlock(t, craftBaseIndex()),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
				return inner
			},
			dest: func() port.FileWriter {
				return &hookFS{Filesystem: testutil.NewMapFS(nil), onCreate: func(string) (io.WriteCloser, error) {
					return nil, boom
				}}
			},
			wantMsg: "создание",
		},
		{
			name: "ошибка записи в целевой файл",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				return craftSessionTape(t,
					craftIndexBlock(t, craftBaseIndex()),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
			},
			dest: func() port.FileWriter {
				return &hookFS{Filesystem: testutil.NewMapFS(nil), onCreate: func(string) (io.WriteCloser, error) {
					w, _ := testutil.NewMapFS(nil).Create("/sink")
					return &hookWriter{WriteCloser: w, onWrite: func() error { return boom }}, nil
				}}
			},
			wantMsg: "запись",
		},
		{
			name: "ошибка закрытия целевого файла",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				return craftSessionTape(t,
					craftIndexBlock(t, craftBaseIndex()),
					craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
			},
			dest: func() port.FileWriter {
				return &hookFS{Filesystem: testutil.NewMapFS(nil), onCreate: func(string) (io.WriteCloser, error) {
					w, _ := testutil.NewMapFS(nil).Create("/sink")
					return &hookWriter{WriteCloser: w, onClose: func() error { return boom }}, nil
				}}
			},
			wantMsg: "закрытие",
		},
		{
			name: "ошибка чтения блока в середине файла",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				big := strings.Repeat("x", 300*1024) // tar займёт 2 блока
				idx := craftBaseIndex()
				idx.Files[0].Size = int64(len(big))
				idx.Files[0].Hash = hashOf(big)
				inner := craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/f.txt", size: int64(len(big)), content: big}))
				return &hookTape{Tape: inner, onRead: func(c int) error {
					if c == 4 { // 1=индекс, 2=filemark, 3,4=блоки tar
						return boom
					}
					return nil
				}}
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "чтение",
		},
		{
			name: "ошибка дочитывания сегмента (drain)",
			tape: func(t *testing.T) port.Tape {
				t.Helper()
				big := strings.Repeat("x", 300*1024)
				idx := craftBaseIndex()
				idx.Files[0].Size = int64(len(big))
				idx.Files[0].Hash = hashOf(big)
				inner := craftSessionTape(t, craftIndexBlock(t, idx),
					craftTar(t, tarEntry{name: "/f.txt", size: int64(len(big)), content: big}))
				return &hookTape{Tape: inner, onRead: func(c int) error {
					if c == 5 { // финальный filemark после tar
						return boom
					}
					return nil
				}}
			},
			dest:    func() port.FileWriter { return testutil.NewMapFS(nil) },
			wantMsg: "дочитывание",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dest port.FileWriter
			if tt.dest != nil {
				dest = tt.dest()
			}
			files, err := tapeformat.ReadSession(context.Background(), tt.tape(t), dest, nil, nil)
			if err == nil {
				t.Fatalf("err = nil, files = %+v", files)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %T", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %v, want вхождение %q", err, tt.wantMsg)
			}
		})
	}
}

func TestReadSession_CancelBetweenEntries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	idx := craftBaseIndex()
	tarBytes := craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"})
	tape := craftSessionTape(t, craftIndexBlock(t, idx), tarBytes)

	// Close восстановленного файла отменяет контекст: extractEntry
	// завершится чисто, а следующий виток цикла readTar увидит отмену.
	dest := &hookFS{Filesystem: testutil.NewMapFS(nil), onCreate: func(p string) (io.WriteCloser, error) {
		w, err := testutil.NewMapFS(nil).Create(p)
		if err != nil {
			return nil, err
		}
		return &hookWriter{WriteCloser: w, onClose: func() error {
			cancel()
			return nil
		}}, nil
	}}
	_, err := tapeformat.ReadSession(ctx, tape, dest, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "чтение tar") {
		t.Fatalf("err = %v, want «чтение tar: … context canceled»", err)
	}
}

func TestReadSession_EmptyTarBlock(t *testing.T) {
	ctx := context.Background()
	// Пустой блок в сегменте tar: reader должен прозрачно взять следующий.
	tape := testutil.NewFakeTape()
	idx := craftBaseIndex()
	idx.Files = []domain.FileMeta{}
	_ = tape.WriteBlock(ctx, craftIndexBlock(t, idx))
	_ = tape.WriteEOF(ctx)
	_ = tape.WriteBlock(ctx, nil) // пустой блок
	_ = tape.WriteEOF(ctx)
	_ = tape.Rewind(ctx)

	files, err := tapeformat.ReadSession(ctx, tape, testutil.NewMapFS(nil), nil, nil)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("files = %+v, want пусто", files)
	}
}

// readAllFrom читает файл целиком через port.FileReader.
func readAllFrom(fs port.FileReader, path string) (string, error) {
	r, err := fs.Open(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// TestReadSession_LinkExtractionErrors — ветки извлечения symlink и
// hardlink: несовпадение типа с индексом, сбой каталога и самой ссылки.
func TestReadSession_LinkExtractionErrors(t *testing.T) {
	// ссылки лежат в /sub: сбой MkdirAll инъектируется точечно по
	// каталогу /sub, не задевая обычные файлы сессии
	symlinkSession := func(t *testing.T) *testutil.FakeTape {
		t.Helper()
		idx := craftBaseIndex()
		idx.Files = append(idx.Files, domain.FileMeta{
			Path: "/sub/link", Type: domain.FileTypeSymlink, Linkname: "target", State: domain.StateAdded,
		})
		tarBytes := craftTar(t,
			tarEntry{name: "/f.txt", size: 3, content: "abc"},
			tarEntry{name: "/sub/link", typeflag: tar.TypeSymlink, link: "target"},
		)
		return craftSessionTape(t, craftIndexBlock(t, idx), tarBytes)
	}
	hardlinkSession := func(t *testing.T) *testutil.FakeTape {
		t.Helper()
		idx := craftBaseIndex()
		idx.Files = append(idx.Files, domain.FileMeta{
			Path: "/sub/second", Type: domain.FileTypeHardlink, Linkname: "/f.txt", State: domain.StateAdded,
		})
		tarBytes := craftTar(t,
			tarEntry{name: "/f.txt", size: 3, content: "abc"},
			tarEntry{name: "/sub/second", typeflag: tar.TypeLink, link: "/f.txt"},
		)
		return craftSessionTape(t, craftIndexBlock(t, idx), tarBytes)
	}
	boom := errors.New("boom")
	linkFS := func(hooks func(h *hookFS)) port.FileWriter {
		fs := &hookFS{Filesystem: testutil.NewMapFS(nil)}
		hooks(fs)
		return fs
	}
	// сбой MkdirAll только по каталогу ссылок: обычные файлы сессии
	// (/f.txt, MkdirAll ".") обрабатываются раньше и не должны
	// перехватывать инъекцию
	failSubMkdir := func(h *hookFS) {
		h.onMkdir = func(p string) error {
			if p == "/sub" {
				return boom
			}
			return nil
		}
	}

	t.Run("symlink: сбой MkdirAll", func(t *testing.T) {
		_, err := tapeformat.ReadSession(context.Background(), symlinkSession(t),
			linkFS(failSubMkdir), nil, nil)
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "каталог для") {
			t.Fatalf("err = %v; want MkdirAll-сбой для symlink", err)
		}
	})
	t.Run("symlink: сбой Symlink", func(t *testing.T) {
		_, err := tapeformat.ReadSession(context.Background(), symlinkSession(t),
			linkFS(func(h *hookFS) { h.onSymlink = func(string) error { return boom } }), nil, nil)
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("err = %v; want Symlink-сбой", err)
		}
	})
	t.Run("hardlink: сбой MkdirAll", func(t *testing.T) {
		_, err := tapeformat.ReadSession(context.Background(), hardlinkSession(t),
			linkFS(failSubMkdir), nil, nil)
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "каталог для") {
			t.Fatalf("err = %v; want MkdirAll-сбой для hardlink", err)
		}
	})
	t.Run("hardlink: сбой Link", func(t *testing.T) {
		_, err := tapeformat.ReadSession(context.Background(), hardlinkSession(t),
			linkFS(func(h *hookFS) { h.onLink = func(string) error { return boom } }), nil, nil)
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "hardlink") {
			t.Fatalf("err = %v; want Link-сбой", err)
		}
	})
	t.Run("hardlink: тип записи не совпал с индексом", func(t *testing.T) {
		idx := craftBaseIndex()
		// индекс: symlink; tar: TypeLink — extractHardlink видит чужой тип
		idx.Files = append(idx.Files, domain.FileMeta{
			Path: "/second", Type: domain.FileTypeSymlink, Linkname: "/f.txt", State: domain.StateAdded,
		})
		tarBytes := craftTar(t,
			tarEntry{name: "/f.txt", size: 3, content: "abc"},
			tarEntry{name: "/second", typeflag: tar.TypeLink, link: "/f.txt"},
		)
		tape := craftSessionTape(t, craftIndexBlock(t, idx), tarBytes)
		_, err := tapeformat.ReadSession(context.Background(), tape, testutil.NewMapFS(nil), nil, nil)
		var dmg *domain.SessionDamageError
		if !errors.As(err, &dmg) || !strings.Contains(err.Error(), "неожидаемый тип или linkname") {
			t.Fatalf("err = %v; want повреждение (тип записи)", err)
		}
	})
	t.Run("неожидаемый тип tar", func(t *testing.T) {
		idx := craftBaseIndex()
		tarBytes := craftTar(t, tarEntry{name: "/f.txt", typeflag: tar.TypeFifo})
		tape := craftSessionTape(t, craftIndexBlock(t, idx), tarBytes)
		_, err := tapeformat.ReadSession(context.Background(), tape, testutil.NewMapFS(nil), nil, nil)
		var dmg *domain.SessionDamageError
		if !errors.As(err, &dmg) || !strings.Contains(err.Error(), "неожидаемый тип") {
			t.Fatalf("err = %v; want повреждение (тип tar)", err)
		}
	})
	t.Run("обычный файл с linkname в индексе", func(t *testing.T) {
		idx := craftBaseIndex()
		idx.Files[0].Linkname = "target" // reg с linkname — Validate падает
		tape := craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t, tarEntry{name: "/f.txt", size: 3, content: "abc"}))
		_, err := tapeformat.ReadSession(context.Background(), tape, nil, nil, nil)
		var dmg *domain.SessionDamageError
		if !errors.As(err, &dmg) || !strings.Contains(err.Error(), "linkname") {
			t.Fatalf("err = %v; want повреждение (linkname у обычного файла)", err)
		}
	})
	t.Run("ссылки в режиме проверки (dest nil)", func(t *testing.T) {
		idx := craftBaseIndex()
		idx.Files = append(idx.Files,
			domain.FileMeta{Path: "/s", Type: domain.FileTypeSymlink, Linkname: "t", State: domain.StateAdded},
			domain.FileMeta{Path: "/h", Type: domain.FileTypeHardlink, Linkname: "/f.txt", State: domain.StateAdded},
		)
		tarBytes := craftTar(t,
			tarEntry{name: "/f.txt", size: 3, content: "abc"},
			tarEntry{name: "/s", typeflag: tar.TypeSymlink, link: "t"},
			tarEntry{name: "/h", typeflag: tar.TypeLink, link: "/f.txt"},
		)
		tape := craftSessionTape(t, craftIndexBlock(t, idx), tarBytes)
		files, err := tapeformat.ReadSession(context.Background(), tape, nil, nil, nil)
		if err != nil {
			t.Fatalf("ReadSession (dest=nil): %v", err)
		}
		if len(files) != 3 {
			t.Fatalf("файлов %d; want 3", len(files))
		}
	})
}

// TestWriteSession_InvalidLinkname — валидация индекса на записи:
// обычный файл с linkname не попадает на ленту.
func TestWriteSession_InvalidLinkname(t *testing.T) {
	idx := craftBaseIndex()
	idx.Files[0].Linkname = "target"
	err := tapeformat.WriteSession(context.Background(), testutil.NewFakeTape(), idx, testutil.NewMapFS(nil), nil)
	if err == nil || !strings.Contains(err.Error(), "linkname") {
		t.Fatalf("WriteSession: %v; want ошибка валидации", err)
	}
}
