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
	"errors"
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

func TestCodec_ImplementsPort(t *testing.T) {
	// Присваивание переменной типа port.TapeCodec не скомпилируется,
	// если Codec перестанет реализовывать порт.
	var codec port.TapeCodec = tapeformat.NewCodec()
	codec.EncodeLabel(domain.TapeLabel{})
}

func TestCodec_LabelRoundtrip(t *testing.T) {
	codec := tapeformat.NewCodec()
	label := domain.TapeLabel{
		Magic:         domain.Magic,
		FormatVersion: domain.FormatVersion,
		Name:          "rt",
		UUID:          "u-1",
		FormattedAt:   "2026-08-15T00:00:00Z",
	}
	block, err := codec.EncodeLabel(label)
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	if len(block) != domain.BlockSize {
		t.Fatalf("блок ярлыка %d байт; want %d", len(block), domain.BlockSize)
	}
	got, err := codec.DecodeLabel(block)
	if err != nil {
		t.Fatalf("DecodeLabel: %v", err)
	}
	if got != label {
		t.Fatalf("roundtrip: %+v; want %+v", got, label)
	}
}

func TestCodec_SessionRoundtrip(t *testing.T) {
	codec := tapeformat.NewCodec()
	tape := testutil.NewFakeTape()
	content := "127.0.0.1 localhost\n"
	fs := testutil.NewMapFS(map[string]string{"etc/hosts": content})

	header := port.SessionHeader{
		SessionNum: 1,
		Type:       domain.SessionFull,
		JobRunID:   "run-1",
		Timestamp:  1700000000,
		JobName:    "daily",
	}
	files := []domain.FileMeta{
		{Path: "/etc", IsDir: true, State: domain.StateAdded},
		{
			Path:    "/etc/hosts",
			Size:    int64(len(content)),
			ModTime: 1,
			Hash:    fmt.Sprintf("%016x", xxhash.Sum64String(content)),
			State:   domain.StateAdded,
		},
	}
	if err := codec.WriteSession(context.Background(), tape, header, files, fs, nil); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	if err := tape.Rewind(context.Background()); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	got, err := codec.ReadSession(context.Background(), tape, nil, nil, nil)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(got) != 2 || got[1].Path != "/etc/hosts" {
		t.Fatalf("прочитанные файлы: %+v", got)
	}
}

func TestCodec_ReadHeader(t *testing.T) {
	codec := tapeformat.NewCodec()
	ctx := context.Background()

	t.Run("заголовок части с continues", func(t *testing.T) {
		tape := testutil.NewFakeTape()
		fs := testutil.NewMapFS(map[string]string{"a": "x"})
		header := port.SessionHeader{
			SessionNum: 1, Type: domain.SessionFull,
			JobRunID: "run-9", Timestamp: 1700000042, JobName: "media",
			Part: 2, Continues: "uuid-prev",
		}
		files := []domain.FileMeta{{
			Path: "/a", Size: 1, ModTime: 1,
			Hash:  fmt.Sprintf("%016x", xxhash.Sum64String("x")),
			State: domain.StateAdded,
		}}
		if err := codec.WriteSession(ctx, tape, header, files, fs, nil); err != nil {
			t.Fatalf("WriteSession: %v", err)
		}
		if err := tape.Rewind(ctx); err != nil {
			t.Fatalf("Rewind: %v", err)
		}
		got, err := codec.ReadHeader(ctx, tape)
		if err != nil {
			t.Fatalf("ReadHeader: %v", err)
		}
		want := header // Files читателем не заполняются
		if got != want {
			t.Fatalf("ReadHeader: %+v; want %+v", got, want)
		}
	})

	t.Run("пустой индекс", func(t *testing.T) {
		tape := testutil.NewFakeTape()
		if err := tape.WriteEOF(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := codec.ReadHeader(ctx, tape); !errors.Is(err, &domain.EmptyIndexError{}) {
			t.Fatalf("ReadHeader: %v; want EmptyIndexError", err)
		}
	})

	t.Run("указатель продолжения", func(t *testing.T) {
		tape := testutil.NewFakeTape()
		if err := tapeformat.WriteContinuation(ctx, tape, port.Continuation{
			JobRunID: "run", SessionNum: 2, Part: 2, NextTapeName: "T2",
		}); err != nil {
			t.Fatal(err)
		}
		if err := tape.Rewind(ctx); err != nil {
			t.Fatal(err)
		}
		_, err := codec.ReadHeader(ctx, tape)
		var cont *domain.ContinuationError
		if !errors.As(err, &cont) || cont.NextTapeName != "T2" {
			t.Fatalf("ReadHeader: %v; want ContinuationError", err)
		}
	})
}

// TestProgressOr_NilProgNoop — заглушка прогресса проглатывает все
// вызовы (ReadSession с prog == nil).
func TestProgressOr_NilProgNoop(t *testing.T) {
	ctx := context.Background()
	_, idx := singleFileFixture("/f.txt", "data")
	tape := craftSessionTape(t, craftIndexBlock(t, idx), craftTar(t, tarEntry{name: "/f.txt", size: 4, content: "data"}))
	if _, err := tapeformat.ReadSession(ctx, tape, nil, nil, nil); err != nil {
		t.Fatalf("ReadSession без прогресса: %v", err)
	}
}
