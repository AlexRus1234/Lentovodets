package testutil

import (
	"testing"

	"lentovodec/internal/domain"
)

func TestStaticConfig_JobsRoundTrip(t *testing.T) {
	jobs := []domain.Job{{
		Name:  "daily",
		Mode:  domain.ModeMirror,
		Paths: []string{"/tank/data"},
	}}
	c := &StaticConfig{JobList: jobs}

	got, err := c.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(got) != 1 || got[0].Name != "daily" {
		t.Fatalf("Jobs: got %v, want исходный список", got)
	}
	if c.Device() != "" || c.DB() != "" || c.Log() != "" || c.Server() != "" {
		t.Fatal("геттеры путей должны возвращать пустые строки")
	}
	if c.LogLevel() != "info" {
		t.Fatalf("LogLevel: got %q, want info", c.LogLevel())
	}
}
