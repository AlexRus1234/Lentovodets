package xxhash_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"lentovodec/internal/adapter/xxhash"
	"lentovodec/internal/port"
)

func TestHasher_KnownVectors(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "ef46db3751d8e999"},
		{"a", "d24ec4f1a98c6e5b"},
		{"abc", "44bc2cf5ad770999"},
	}
	var h port.Hasher = xxhash.New()
	for _, tc := range cases {
		got, err := h.Hash(strings.NewReader(tc.in))
		if err != nil {
			t.Fatalf("Hash(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("Hash(%q) = %s; want %s", tc.in, got, tc.want)
		}
	}
}

func TestHasher_ReaderError(t *testing.T) {
	boom := errors.New("boom")
	h := xxhash.New()
	if _, err := h.Hash(errReader{boom}); !errors.Is(err, boom) {
		t.Fatalf("Hash: %v; want boom", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

var _ io.Reader = errReader{}
