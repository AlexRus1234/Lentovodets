package tapeformat_test

import (
	"context"
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
	got, err := codec.ReadSession(context.Background(), tape, nil, nil)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(got) != 2 || got[1].Path != "/etc/hosts" {
		t.Fatalf("прочитанные файлы: %+v", got)
	}
}
