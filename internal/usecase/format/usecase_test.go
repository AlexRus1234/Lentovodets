package format_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
	"lentovodec/internal/usecase/format"
)

var fixedTime = time.Unix(1700000000, 0).UTC()

func newUseCase(tape port.Tape, codec port.TapeCodec, cat port.Catalog, rnd port.Rand) *format.UseCase {
	return format.New(tape, codec, cat, rnd, testutil.FixedClock(fixedTime), testutil.NoopLogger())
}

func writeLabelToTape(t *testing.T, tape port.Tape, codec port.TapeCodec, label domain.TapeLabel) {
	t.Helper()
	block, err := codec.EncodeLabel(label)
	if err != nil {
		t.Fatalf("EncodeLabel: %v", err)
	}
	if err := tape.WriteBlock(context.Background(), block); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if err := tape.WriteEOF(context.Background()); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	if err := tape.WriteEOF(context.Background()); err != nil {
		t.Fatalf("WriteEOF(2): %v", err)
	}
}

func TestFormat_BlankTape(t *testing.T) {
	tape := testutil.NewFakeTape()
	cat := testutil.NewMemCatalog()
	uc := newUseCase(tape, &testutil.FakeCodec{}, cat, testutil.FixedRand("uuid-1"))

	label, err := uc.Format(context.Background(), "daily-1", false)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if label.Magic != domain.Magic || label.FormatVersion != domain.FormatVersion {
		t.Errorf("ярлык: %+v", label)
	}
	if label.Name != "daily-1" || label.UUID != "uuid-1" {
		t.Errorf("ярлык: %+v", label)
	}
	if label.FormattedAt != fixedTime.Format(time.RFC3339) {
		t.Errorf("FormattedAt = %q; want %q", label.FormattedAt, fixedTime.Format(time.RFC3339))
	}
	if got := tape.BlockCount(); got != 1 {
		t.Errorf("BlockCount = %d; want 1 (ярлык)", got)
	}
	if got := tape.MarkCount(); got != 2 {
		t.Errorf("MarkCount = %d; want 2 (двойной EOF)", got)
	}
	rec, err := cat.GetTapeByUUID(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("кассета не зарегистрирована: %v", err)
	}
	if rec.Name != "daily-1" || rec.FormattedAt != fixedTime.Unix() {
		t.Errorf("запись каталога: %+v", rec)
	}
}

func TestFormat_AlreadyFormatted(t *testing.T) {
	tape := testutil.NewFakeTape()
	codec := &testutil.FakeCodec{}
	writeLabelToTape(t, tape, codec, domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "old", UUID: "old-uuid",
	})
	uc := newUseCase(tape, codec, testutil.NewMemCatalog(), testutil.FixedRand("new-uuid"))

	_, err := uc.Format(context.Background(), "daily-2", false)
	var already *domain.AlreadyFormattedError
	if !errors.As(err, &already) {
		t.Fatalf("Format: %v; want AlreadyFormattedError", err)
	}
	if already.Name != "old" || already.UUID != "old-uuid" {
		t.Errorf("детали ошибки: %+v", already)
	}
	if got := tape.BlockCount(); got != 1 {
		t.Errorf("лента изменена: BlockCount = %d; want 1", got)
	}
}

func TestFormat_ForceReformats(t *testing.T) {
	tape := testutil.NewFakeTape()
	codec := &testutil.FakeCodec{}
	writeLabelToTape(t, tape, codec, domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion,
		Name: "old", UUID: "old-uuid",
	})
	cat := testutil.NewMemCatalog()
	uc := newUseCase(tape, codec, cat, testutil.FixedRand("new-uuid"))

	label, err := uc.Format(context.Background(), "fresh", true)
	if err != nil {
		t.Fatalf("Format(force): %v", err)
	}
	if label.UUID != "new-uuid" || label.Name != "fresh" {
		t.Fatalf("новый ярлык: %+v", label)
	}
	rec, err := cat.GetTapeByUUID(context.Background(), "new-uuid")
	if err != nil {
		t.Fatalf("новая кассета не зарегистрирована: %v", err)
	}
	if rec.Name != "fresh" {
		t.Errorf("каталог: %+v", rec)
	}
}

func TestFormat_ForeignTapeAllowed(t *testing.T) {
	tape := testutil.NewFakeTape()
	ctx := context.Background()
	if err := tape.WriteBlock(ctx, []byte("someone else's tar data")); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	uc := newUseCase(tape, &testutil.FakeCodec{}, testutil.NewMemCatalog(), testutil.FixedRand("u"))

	if _, err := uc.Format(ctx, "reused", false); err != nil {
		t.Fatalf("Format поверх чужой ленты без force: %v", err)
	}
}

func TestFormat_NewerFormatRejected(t *testing.T) {
	tape := testutil.NewFakeTape()
	codec := &testutil.FakeCodec{}
	writeLabelToTape(t, tape, codec, domain.TapeLabel{
		Magic: domain.Magic, FormatVersion: domain.FormatVersion + 1,
		Name: "future", UUID: "u",
	})
	uc := newUseCase(tape, codec, testutil.NewMemCatalog(), testutil.FixedRand("u"))

	for _, force := range []bool{false, true} {
		if _, err := uc.Format(context.Background(), "x", force); err == nil {
			t.Fatalf("Format(force=%v) поверх новой версии формата должен падать", force)
		}
	}
}

func TestFormat_EmptyName(t *testing.T) {
	uc := newUseCase(testutil.NewFakeTape(), &testutil.FakeCodec{}, testutil.NewMemCatalog(), testutil.FixedRand("u"))
	if _, err := uc.Format(context.Background(), "", false); err == nil {
		t.Fatal("пустое имя должно отвергаться")
	}
}

func TestFormat_RandFailure(t *testing.T) {
	boom := errors.New("boom")
	uc := newUseCase(testutil.NewFakeTape(), &testutil.FakeCodec{}, testutil.NewMemCatalog(), testutil.FailingRand(boom))
	if _, err := uc.Format(context.Background(), "n", false); !errors.Is(err, boom) {
		t.Fatalf("Format: %v; want boom", err)
	}
}

func TestFormat_EncodeFailure(t *testing.T) {
	boom := errors.New("boom")
	codec := &testutil.FakeCodec{ErrEncode: boom}
	uc := newUseCase(testutil.NewFakeTape(), codec, testutil.NewMemCatalog(), testutil.FixedRand("u"))
	if _, err := uc.Format(context.Background(), "n", false); !errors.Is(err, boom) {
		t.Fatalf("Format: %v; want boom", err)
	}
}

func TestFormat_TapeAndCatalogFailures(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		tape port.Tape
		cat  port.Catalog
	}{
		{"rewind fails", &failTape{FakeTape: testutil.NewFakeTape(), rewind: boom}, testutil.NewMemCatalog()},
		{"read fails", &failTape{FakeTape: testutil.NewFakeTape(), read: boom}, testutil.NewMemCatalog()},
		{"write fails", &failTape{FakeTape: testutil.NewFakeTape(), write: boom}, testutil.NewMemCatalog()},
		{"eof fails", &failTape{FakeTape: testutil.NewFakeTape(), eof: boom}, testutil.NewMemCatalog()},
		{"register fails", testutil.NewFakeTape(), &failCatalog{MemCatalog: testutil.NewMemCatalog(), register: boom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newUseCase(tc.tape, &testutil.FakeCodec{}, tc.cat, testutil.FixedRand("u"))
			if _, err := uc.Format(context.Background(), "n", false); !errors.Is(err, boom) {
				t.Fatalf("Format: %v; want boom", err)
			}
		})
	}
}

func TestFormat_CanceledContext(t *testing.T) {
	uc := newUseCase(testutil.NewFakeTape(), &testutil.FakeCodec{}, testutil.NewMemCatalog(), testutil.FixedRand("u"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := uc.Format(ctx, "n", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Format: %v; want context.Canceled", err)
	}
}

// failTape — FakeTape с инъекцией сбоев отдельных операций.
type failTape struct {
	*testutil.FakeTape
	rewind error
	read   error
	write  error
	eof    error
}

func (t *failTape) Rewind(ctx context.Context) error {
	if t.rewind != nil {
		return t.rewind
	}
	return t.FakeTape.Rewind(ctx)
}

func (t *failTape) ReadBlock(ctx context.Context) ([]byte, error) {
	if t.read != nil {
		return nil, t.read
	}
	return t.FakeTape.ReadBlock(ctx)
}

func (t *failTape) WriteBlock(ctx context.Context, b []byte) error {
	if t.write != nil {
		return t.write
	}
	return t.FakeTape.WriteBlock(ctx, b)
}

func (t *failTape) WriteEOF(ctx context.Context) error {
	if t.eof != nil {
		return t.eof
	}
	return t.FakeTape.WriteEOF(ctx)
}

// failCatalog — MemCatalog с инъекцией сбоя RegisterTape.
type failCatalog struct {
	*testutil.MemCatalog
	register error
}

func (c *failCatalog) RegisterTape(ctx context.Context, uuid, name string, formattedAt int64) error {
	if c.register != nil {
		return c.register
	}
	return c.MemCatalog.RegisterTape(ctx, uuid, name, formattedAt)
}
