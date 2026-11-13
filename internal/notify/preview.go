package notify

import (
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Preview is what a channel would have been sent over a stretch of recorded
// changes, for the channel form and the Notifications page.
type Preview struct {
	Messages int     // after each poll, one per poll with a match; plus one per daily digest
	Events   int     // events matched
	Days     float64 // how long the stretch was
	Shown    []Event // a few of the events, newest first, of different kinds where there are
}

// PerDay is the messages a day, on average.
func (p Preview) PerDay() float64 {
	if p.Days <= 0 {
		return 0
	}
	return float64(p.Messages) / p.Days
}

// PreviewChannel works out what a channel with rules, sending after each
// poll (instant) and/or a digest at digestAt ("" for none, in loc), would
// have been sent for changes, as Store.NotifyChangesSince(since) returns
// them, up to now. It keeps show of the events. Platform problems aren't
// counted: they come from failed polls, not changes.
func PreviewChannel(changes []store.NotifyChange, rules Rules, instant bool, digestAt string, loc *time.Location,
	since, now time.Time, show int) Preview {
	p := Preview{Days: now.Sub(since).Hours() / 24}
	var matched []Event
	digests := map[time.Time]bool{}
	send := func(events []Event) {
		n := 0
		for _, e := range events {
			m, ok := rules.Match(e)
			if !ok {
				continue
			}
			n++
			matched = append(matched, m)
			if digestAt != "" {
				// The digest after it: the next slot after the last one before.
				if slot := LastSlot(digestAt, m.At, loc).Add(24 * time.Hour); !slot.After(now) {
					digests[slot] = true
				}
			}
		}
		if instant && n > 0 {
			p.Messages++
		}
	}
	// Each poll's changes are a message, as the notifier sends them.
	for start := 0; start < len(changes); {
		end := start + 1
		for end < len(changes) && changes[start].RunID != 0 && changes[end].RunID == changes[start].RunID {
			end++
		}
		send(FromChanges(changes[start:end]))
		start = end
	}
	// Flip-flopping programs' pauses that lasted, sent a day in.
	for i, c := range changes {
		if c.Kind != store.ChangeStateChanged || c.NewValue == platform.StateOpen || c.Flips < store.FlipFlops {
			continue
		}
		until := now
		for _, next := range changes[i+1:] {
			if next.Kind == store.ChangeStateChanged && next.Program.ID == c.Program.ID {
				until = next.OccurredAt
				break
			}
		}
		if until.Sub(c.OccurredAt) < LongPause {
			continue
		}
		c.LongPause = true
		events := FromChanges([]store.NotifyChange{c})
		for j := range events {
			events[j].At = c.OccurredAt.Add(LongPause)
		}
		send(events)
	}
	p.Messages += len(digests)
	p.Events = len(matched)
	p.Shown = pick(matched, show)
	return p
}

// pick returns up to n events, newest first: the newest of each kind, in
// EventKinds' order (somewhere new to hunt first), then the newest of the
// rest.
func pick(events []Event, n int) []Event {
	byAge := slices.Clone(events)
	slices.SortStableFunc(byAge, func(a, b Event) int { return b.At.Compare(a.At) }) // long pauses are sent late
	var out []Event
	taken := make([]bool, len(byAge))
	for _, k := range EventKinds {
		if i := slices.IndexFunc(byAge, func(e Event) bool { return e.Kind == k.Key }); i >= 0 && len(out) < n {
			taken[i] = true
			out = append(out, byAge[i])
		}
	}
	for i, e := range byAge {
		if len(out) < n && !taken[i] {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Event) int { return b.At.Compare(a.At) })
	return out
}
