package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// A batch of distinct made-up names past MaxBulkLookups is refused before
// any lookup.
func TestMatchRulesBulkCapped(t *testing.T) {
	s := newTestStore(t, func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) })
	var inputs []string
	for i := range MaxBulkLookups/9 + 1 { // 9 new names each (example.com and com are shared)
		inputs = append(inputs, fmt.Sprintf("a%d.b%d.c%d.d%d.e%d.f%d.g%d.h%d.i%d.example.com", i, i, i, i, i, i, i, i, i))
	}
	if _, err := s.MatchRulesBulk(context.Background(), inputs); !errors.Is(err, ErrTooManyLookups) {
		t.Errorf("err %v, want ErrTooManyLookups", err)
	}
	if _, err := s.MatchRulesBulk(context.Background(), inputs[:100]); err != nil {
		t.Errorf("a small batch: %v", err)
	}
}
