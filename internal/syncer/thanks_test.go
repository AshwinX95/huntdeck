package syncer

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// stubThanks answers FetchThanks from list: everything when known is nil,
// else only the hunters listed in top (as if the rest matched known).
type stubThanks struct {
	list  map[string]int
	top   []string
	calls []bool // whether each call had known
}

func (s *stubThanks) FetchThanks(_ context.Context, _ string, known map[string]int) (platform.Thanks, error) {
	s.calls = append(s.calls, known != nil)
	if known == nil {
		return platform.Thanks{Shown: true, Hunters: maps.Clone(s.list), Listed: len(s.list), Complete: true}, nil
	}
	t := platform.Thanks{Shown: true, Hunters: map[string]int{}, Listed: len(s.list)}
	for _, h := range s.top {
		t.Hunters[h] = s.list[h]
	}
	return t, nil
}

func TestThanksCountsValidReports(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)
	ctx := context.Background()
	ids, err := e.store.LiveProgramIDs(ctx, e.fake.Name())
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(_ context.Context, handles []string, save platform.StatsSaver) error {
		return save(handles, map[string]platform.ProgramStats{"acme": {TotalReports: ptr(50)}})
	}
	stub := &stubThanks{list: map[string]int{"a": 3, "b": 1, "c": 2}}
	accepted := func() int {
		t.Helper()
		if _, _, err := e.sync.fetchStats(ctx, fetch, stub, 0, []string{"acme"}, ids); err != nil {
			t.Fatal(err)
		}
		st, err := e.store.LatestProgramStats(ctx, ids["acme"])
		if err != nil || st == nil || st.AcceptedReports == nil {
			t.Fatalf("stats = %+v, %v", st, err)
		}
		return *st.AcceptedReports
	}

	steps := []struct {
		name  string
		after time.Duration
		edit  func()
		want  int
		calls []bool
	}{
		{"first read is whole", 0, func() {}, 6, []bool{false}},
		{"a rise read from the top", 24 * time.Hour, func() { stub.list["b"], stub.top = 4, []string{"b"} }, 9, []bool{true}},
		{"someone new further down: read again", 24 * time.Hour, func() { stub.list["d"], stub.top = 1, nil }, 10, []bool{true, false}},
		{"weekly whole read", 8 * 24 * time.Hour, func() { stub.list["a"] = 5 }, 12, []bool{false}},
	}
	for _, s := range steps {
		e.clock = e.clock.Add(s.after)
		s.edit()
		stub.calls = nil
		if got := accepted(); got != s.want || !slices.Equal(stub.calls, s.calls) {
			t.Errorf("%s: accepted %d (want %d), calls %v (want %v)", s.name, got, s.want, stub.calls, s.calls)
		}
	}
}
