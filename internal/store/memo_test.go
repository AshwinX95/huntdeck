package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A memoised result is reused until anything is written to the database
// or MemoTTL passes; callers asking while it's worked out wait for it.
func TestMemo(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	runs := 0
	read := func() int {
		t.Helper()
		v, err := Memo(ctx, s, "k", func(context.Context) (int, error) { runs++; return runs, nil })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if first, again := read(), read(); first != 1 || again != 1 {
		t.Fatalf("not reused while nothing changed: %d, %d", first, again)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.PutSetting(ctx, "memo-test", "x") }); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 2 {
		t.Fatalf("after a write: %d, want worked out again", got)
	}
	now = now.Add(MemoTTL)
	if got := read(); got != 3 {
		t.Fatalf("after MemoTTL: %d, want worked out again", got)
	}

	release := make(chan struct{})
	var wg sync.WaitGroup
	got := make([]int, 3)
	calls := 0
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := Memo(ctx, s, "slow", func(context.Context) (int, error) { calls++; <-release; return 7, nil })
			if err != nil {
				t.Error(err)
			}
			got[i] = v
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 || got[0] != 7 || got[1] != 7 || got[2] != 7 {
		t.Errorf("three callers at once: worked out %d times, got %v", calls, got)
	}
}
