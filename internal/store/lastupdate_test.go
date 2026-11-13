package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// With no polls (a demo, an install loaded from a seed), the last update
// is when a platform last listed a program.
func TestLastUpdateWithoutPolls(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	if at, err := s.LastUpdate(ctx); err != nil || at != nil {
		t.Fatalf("empty: %v, %v", at, err)
	}
	err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if at, err := s.LastUpdate(ctx); err != nil || at == nil || !at.Equal(now) {
		t.Errorf("programs only: %v, %v; want %v", at, err, now)
	}
}
