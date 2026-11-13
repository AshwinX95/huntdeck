package syncer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// BenchmarkPoll measures the database side of a poll with the network taken
// out: 600 programs × 60 scope entries (≈ a real HackerOne account).
//
//	go test -run '^$' -bench Poll -benchtime 3x ./internal/syncer/
func BenchmarkPoll(b *testing.B) {
	const programs, entries = 600, 60
	setup := func(b *testing.B) *env {
		e := newEnv(b)
		var ps []platform.Program
		for i := range programs {
			h := fmt.Sprintf("prog%03d", i)
			ps = append(ps, prog(h, i%5 == 0))
			var es []platform.ScopeEntry
			for j := range entries {
				es = append(es, platform.ScopeEntry{PlatformID: fmt.Sprintf("%d-%d", i, j), InScope: j%4 != 0,
					AssetType: scope.AssetURL, Identifier: fmt.Sprintf("h%d.p%d.example.com", j, i), BountyEligible: true})
			}
			e.fake.SetScope(h, es...)
		}
		e.fake.SetPrograms(ps...)
		return e
	}

	b.Run("first-import", func(b *testing.B) {
		for range b.N {
			b.StopTimer()
			e := setup(b)
			b.StartTimer()
			if _, err := e.sync.Poll(context.Background(), e.fake, Options{Mode: ModeFull, Trigger: "bench"}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("unchanged-repoll", func(b *testing.B) {
		e := setup(b)
		if _, err := e.sync.Poll(context.Background(), e.fake, Options{Mode: ModeFull, Trigger: "bench"}); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for range b.N {
			e.clock = e.clock.Add(time.Minute)
			if _, err := e.sync.Poll(context.Background(), e.fake, Options{Mode: ModeFull, Trigger: "bench"}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
