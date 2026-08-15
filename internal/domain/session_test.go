package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"lentovodec/internal/domain"
)

func TestSessionTypeValid(t *testing.T) {
	tests := []struct {
		typ  domain.SessionType
		want bool
	}{
		{domain.SessionFull, true},
		{domain.SessionInc, true},
		{domain.SessionType(""), false},
		{domain.SessionType("full"), false},
		{domain.SessionType("DIFF"), false},
	}
	for _, tt := range tests {
		if got := tt.typ.Valid(); got != tt.want {
			t.Errorf("SessionType(%q).Valid() = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestSessionTypeWireValues(t *testing.T) {
	if string(domain.SessionFull) != "FULL" {
		t.Errorf("SessionFull = %q, want %q", domain.SessionFull, "FULL")
	}
	if string(domain.SessionInc) != "INC" {
		t.Errorf("SessionInc = %q, want %q", domain.SessionInc, "INC")
	}
}

func TestSessionJSON(t *testing.T) {
	sess := domain.Session{
		ID:        7,
		TapeUUID:  "550e8400-e29b-41d4-a716-446655440000",
		Num:       3,
		Type:      domain.SessionInc,
		Timestamp: 1691928896,
		JobRunID:  "0f4c2b18-9b3e-4d1a-8b1f-3f9b6e2a7c11",
	}
	// Сессия не хранится на ленте как есть (там SessionIndex из
	// adapter/tapeformat); фиксируем только сериализуемость и тип Type.
	b, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if want := `"Type":"INC"`; !strings.Contains(string(b), want) {
		t.Errorf("json %s не содержит %s", b, want)
	}
}
