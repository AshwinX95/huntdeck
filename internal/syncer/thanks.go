package syncer

import (
	"context"
	"maps"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// thanksFull is how often a program's whole Thanks list is read again: the
// daily read stops at the first page it already knows, which misses
// changes further down if the list isn't in the order it's taken to be.
const thanksFull = 7 * 24 * time.Hour

// thanksRead is what one read of a program's Thanks list gave.
type thanksRead struct {
	hunters  map[string]int // to save
	complete bool           // hunters is the whole list
	total    int            // the program's valid reports
}

// readThanks reads a program's Thanks list, only as far as it changed when
// the saved one is recent; nil when the program doesn't show one.
func (s *Syncer) readThanks(ctx context.Context, tf platform.ThanksFetcher, programID int64, handle string) (*thanksRead, error) {
	known, readAt, err := s.store.Thanks(ctx, programID)
	if err != nil {
		return nil, err
	}
	if readAt == nil || s.store.Now().Sub(*readAt) >= thanksFull {
		known = nil
	}
	t, err := tf.FetchThanks(ctx, handle, known)
	if err != nil {
		return nil, err
	}
	merged := t.Hunters
	if !t.Complete && known != nil {
		merged = maps.Clone(known)
		maps.Copy(merged, t.Hunters)
		if len(merged) != t.Listed { // someone new further down, or someone gone
			if t, err = tf.FetchThanks(ctx, handle, nil); err != nil {
				return nil, err
			}
			merged = t.Hunters
		}
	}
	if !t.Shown || !t.Complete && known == nil {
		return nil, nil // no list, or one too long to read
	}
	r := &thanksRead{hunters: t.Hunters, complete: t.Complete}
	for _, n := range merged {
		r.total += n
	}
	return r, nil
}

// readAllThanks reads the Thanks lists of the programs in stats, keyed by
// handle. A list that can't be read is left out, so its program's valid
// reports aren't saved that day.
func (s *Syncer) readAllThanks(ctx context.Context, tf platform.ThanksFetcher, stats map[string]platform.ProgramStats,
	ids map[string]int64) (map[string]*thanksRead, error) {
	out := map[string]*thanksRead{}
	if tf == nil {
		return out, nil
	}
	for h := range stats {
		id, ok := ids[h]
		if !ok {
			continue
		}
		r, err := s.readThanks(ctx, tf, id, h)
		if err != nil {
			if ctx.Err() != nil {
				return out, err
			}
			s.log.Warn("thanks list not read", "program", h, "err", err)
			continue
		}
		if r != nil {
			out[h] = r
		}
	}
	return out, nil
}
