// Package fake is an in-memory platform.Platform for tests.
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Platform serves whatever programs and scopes the test sets.
type Platform struct {
	mu              sync.Mutex
	programs        []platform.Program
	scopes          map[string][]platform.ScopeEntry
	listErr         error
	listPart        bool // return listErr with the programs, as a partial listing
	scopeErr        map[string]error
	requests        int64
	scopeHits       map[string]int
	stats           map[string]platform.ProgramStats
	statsErr        error
	statsAsks       [][]string
	changed         map[string]time.Time // for ScopeChangedAt
	checks          map[string]int
	since           *time.Time
	disclosed       []platform.Disclosure
	discAsks        []string // "*" for the all-programs feed, else the handle
	discErr         error
	history         map[string][]platform.ScopeVersion
	histAsks        []string
	states          map[string][]platform.StateChange
	stateAsks       []string
	resolved        []platform.Resolution
	resAsks         []string
	accepted        []platform.AcceptedReport
	accSince        time.Time
	accAsks         []string
	recent          []platform.Program
	recentAsks      int
	details         map[string]platform.ScopeDetail
	webStats        map[string]platform.ProgramStats
	webAsks         [][]string
	invites         []platform.Invite
	invitesErr      error
	acceptedInvites []string
	account         string
}

// DisclosurePage is how many disclosures the fake returns per page.
const DisclosurePage = 2

// New returns an empty fake platform.
func New() *Platform {
	return &Platform{scopes: map[string][]platform.ScopeEntry{}, scopeErr: map[string]error{}, scopeHits: map[string]int{},
		stats: map[string]platform.ProgramStats{}, changed: map[string]time.Time{}, checks: map[string]int{}}
}

// Name implements platform.Platform.
func (f *Platform) Name() string { return "fake" }

// SetAccount sets the account the fake says it lists programs for.
func (f *Platform) SetAccount(account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.account = account
}

// Account implements platform.AccountNamer: "" (can't tell) unless
// SetAccount was called.
func (f *Platform) Account() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.account
}

// SetPrograms replaces the listing.
func (f *Platform) SetPrograms(p ...platform.Program) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.programs = p
}

// SetScope replaces one program's scope.
func (f *Platform) SetScope(handle string, e ...platform.ScopeEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes[handle] = e
}

// SetScopeDetail sets the rules and bounty table that come with handle's
// scope (see Detailed).
func (f *Platform) SetScopeDetail(handle string, d platform.ScopeDetail) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.details == nil {
		f.details = map[string]platform.ScopeDetail{}
	}
	f.details[handle] = d
}

// Detailed is the fake with platform.ScopeDetailFetcher, so tests opt in
// to scope downloads carrying rules and rewards.
type Detailed struct{ *Platform }

// FetchScopeDetail implements platform.ScopeDetailFetcher.
func (d Detailed) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	entries, err := d.FetchScope(ctx, p)
	if err != nil {
		return nil, platform.ScopeDetail{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return entries, d.details[p.Handle], nil
}

// FailListing makes ListPrograms fail. If partial, the programs are still
// returned alongside the error.
func (f *Platform) FailListing(err error, partial bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErr, f.listPart = err, partial
}

// FailScope makes FetchScope fail for one handle (nil clears it).
func (f *Platform) FailScope(handle string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopeErr[handle] = err
}

// ScopeHits returns how many times FetchScope was called for handle.
func (f *Platform) ScopeHits(handle string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scopeHits[handle]
}

// SetScopeChangedAt sets what ScopeChangedAt reports for handle.
func (f *Platform) SetScopeChangedAt(handle string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changed[handle] = t
}

// SetChangesSince sets the listing's ChangesSince (nil clears it).
func (f *Platform) SetChangesSince(t *time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = t
}

// Checks returns how many times ScopeChangedAt was called for handle.
func (f *Platform) Checks(handle string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks[handle]
}

// ScopeChangedAt implements platform.ScopeChangeChecker; it reports the zero
// time unless SetScopeChangedAt was called for the program.
func (f *Platform) ScopeChangedAt(_ context.Context, p platform.Program) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	f.checks[p.Handle]++
	return f.changed[p.Handle], nil
}

// SetHistory sets a program's scope versions for ScopeHistory; programs
// without one are skipped, as platforms do with ones they can't read.
func (f *Platform) SetHistory(handle string, v ...platform.ScopeVersion) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.history == nil {
		f.history = map[string][]platform.ScopeVersion{}
	}
	f.history[handle] = v
}

// HistoryRequests returns the handles ScopeHistory was asked for.
func (f *Platform) HistoryRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.histAsks...)
}

// ScopeHistory implements platform.ScopeHistorian.
func (f *Platform) ScopeHistory(_ context.Context, programs []platform.Program, save platform.ScopeHistorySaver) error {
	for _, p := range programs {
		f.mu.Lock()
		f.requests++
		f.histAsks = append(f.histAsks, p.Handle)
		v, ok := f.history[p.Handle]
		f.mu.Unlock()
		if !ok {
			continue
		}
		if err := save(p.Handle, v); err != nil {
			return err
		}
	}
	return nil
}

// SetStateHistory sets a program's past state changes for StateHistory.
func (f *Platform) SetStateHistory(handle string, c ...platform.StateChange) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = map[string][]platform.StateChange{}
	}
	f.states[handle] = c
}

// StateHistoryRequests returns the handles StateHistory was asked for.
func (f *Platform) StateHistoryRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stateAsks...)
}

// StateHistory implements platform.StateHistorian.
func (f *Platform) StateHistory(_ context.Context, programs []platform.Program, save platform.StateHistorySaver) error {
	for _, p := range programs {
		f.mu.Lock()
		f.stateAsks = append(f.stateAsks, p.Handle)
		c, ok := f.states[p.Handle]
		f.mu.Unlock()
		if !ok {
			continue
		}
		if err := save(p.Handle, c); err != nil {
			return err
		}
	}
	return nil
}

// SetResolutions replaces the resolved, undisclosed reports.
func (f *Platform) SetResolutions(r ...platform.Resolution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = r
}

// ResolutionRequests returns what each FetchResolutions page asked for:
// "*" for the all-programs feed, else the program handle.
func (f *Platform) ResolutionRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resAsks...)
}

// FetchResolutions implements platform.ResolutionFetcher, latest activity
// first, DisclosurePage per page.
func (f *Platform) FetchResolutions(_ context.Context, p *platform.Program, maxPages int, save platform.ResolutionSaver) error {
	f.mu.Lock()
	var all []platform.Resolution
	for _, r := range f.resolved {
		if p == nil || r.ProgramHandle == p.Handle {
			all = append(all, r)
		}
	}
	f.mu.Unlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].ActivityAt.After(all[j].ActivityAt) })
	for page := 0; page < maxPages; page++ {
		f.mu.Lock()
		f.requests++
		ask := "*"
		if p != nil {
			ask = p.Handle
		}
		f.resAsks = append(f.resAsks, ask)
		f.mu.Unlock()
		lo := min(page*DisclosurePage, len(all))
		hi := min(lo+DisclosurePage, len(all))
		more, err := save(append([]platform.Resolution(nil), all[lo:hi]...))
		if err != nil || !more || hi-lo < DisclosurePage {
			return err
		}
	}
	return nil
}

// SetAccepted replaces the accepted reports and the feed's window start;
// see Accepting.
func (f *Platform) SetAccepted(since time.Time, r ...platform.AcceptedReport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepted, f.accSince = append([]platform.AcceptedReport{}, r...), since
}

// AcceptedRequests returns what each FetchAccepted page asked for: "*" for
// the all-programs feed, else the program handle.
func (f *Platform) AcceptedRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.accAsks...)
}

// SetRecent sets what ListRecent returns; see Recent.
func (f *Platform) SetRecent(p ...platform.Program) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recent = p
}

// Recent is a fake platform that can also list its newest programs, as
// Bugcrowd does.
type Recent struct{ *Platform }

// ListRecent implements platform.RecentLister.
func (r Recent) ListRecent(context.Context) (platform.Listing, error) {
	f := r.Platform
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	f.recentAsks++
	return platform.Listing{Programs: append([]platform.Program(nil), f.recent...)}, nil
}

// RecentRequests returns how many times ListRecent was called.
func (f *Platform) RecentRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recentAsks
}

// Accepting is a fake platform that also lists accepted reports, as
// Bugcrowd does. Platform alone doesn't, so polls in tests that don't care
// don't read (and mark as read) accepted-report feeds.
type Accepting struct{ *Platform }

// FetchAccepted implements platform.AcceptedFetcher, latest accepted first,
// DisclosurePage per page.
func (a Accepting) FetchAccepted(_ context.Context, p *platform.Program, maxPages int, save platform.AcceptedSaver) error {
	f := a.Platform
	f.mu.Lock()
	var all []platform.AcceptedReport
	for _, r := range f.accepted {
		if p == nil || r.ProgramHandle == p.Handle {
			all = append(all, r)
		}
	}
	since := f.accSince
	f.mu.Unlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].AcceptedAt.After(all[j].AcceptedAt) })
	for page := 0; page < maxPages; page++ {
		f.mu.Lock()
		f.requests++
		ask := "*"
		if p != nil {
			ask = p.Handle
		}
		f.accAsks = append(f.accAsks, ask)
		f.mu.Unlock()
		lo := min(page*DisclosurePage, len(all))
		hi := min(lo+DisclosurePage, len(all))
		more, err := save(platform.AcceptedPage{Reports: append([]platform.AcceptedReport(nil), all[lo:hi]...), Since: since})
		if err != nil || !more || hi-lo < DisclosurePage {
			return err
		}
	}
	return nil
}

// SetDisclosures replaces the disclosed reports.
func (f *Platform) SetDisclosures(d ...platform.Disclosure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disclosed = d
}

// FailDisclosures makes FetchDisclosures fail (nil clears it).
func (f *Platform) FailDisclosures(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discErr = err
}

// DisclosureRequests returns what each FetchDisclosures page asked for:
// "*" for the all-programs feed, else the program handle.
func (f *Platform) DisclosureRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.discAsks...)
}

// FetchDisclosures implements platform.DisclosureFetcher, newest first,
// DisclosurePage per page.
func (f *Platform) FetchDisclosures(_ context.Context, p *platform.Program, maxPages int, save platform.DisclosureSaver) error {
	f.mu.Lock()
	var all []platform.Disclosure
	for _, d := range f.disclosed {
		if p == nil || d.ProgramHandle == p.Handle {
			all = append(all, d)
		}
	}
	failErr := f.discErr
	f.mu.Unlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].DisclosedAt.After(all[j].DisclosedAt) })
	for page := 0; page < maxPages; page++ {
		f.mu.Lock()
		f.requests++
		ask := "*"
		if p != nil {
			ask = p.Handle
		}
		f.discAsks = append(f.discAsks, ask)
		f.mu.Unlock()
		if failErr != nil {
			return failErr
		}
		lo := min(page*DisclosurePage, len(all))
		hi := min(lo+DisclosurePage, len(all))
		more, err := save(append([]platform.Disclosure(nil), all[lo:hi]...))
		if err != nil || !more || hi-lo < DisclosurePage {
			return err
		}
	}
	return nil
}

// ListPrograms implements platform.Platform.
func (f *Platform) ListPrograms(context.Context) (platform.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.listErr != nil {
		if f.listPart {
			return platform.Listing{Programs: append([]platform.Program(nil), f.programs...)}, f.listErr
		}
		return platform.Listing{}, f.listErr
	}
	return platform.Listing{Programs: append([]platform.Program(nil), f.programs...), Complete: true, ChangesSince: f.since}, nil
}

// FetchScope implements platform.Platform.
func (f *Platform) FetchScope(_ context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	f.scopeHits[p.Handle]++
	if err := f.scopeErr[p.Handle]; err != nil {
		return nil, err
	}
	s, ok := f.scopes[p.Handle]
	if !ok {
		return nil, fmt.Errorf("fake: no scope for %s", p.Handle)
	}
	return append([]platform.ScopeEntry(nil), s...), nil
}

// SetStats sets one program's public stats.
func (f *Platform) SetStats(handle string, s platform.ProgramStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats[handle] = s
}

// FailStats makes FetchProgramStats fail after the first program (nil
// clears it).
func (f *Platform) FailStats(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsErr = err
}

// StatsRequests returns the handles of each FetchProgramStats call.
func (f *Platform) StatsRequests() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.statsAsks...)
}

// FetchProgramStats implements platform.StatsFetcher, one program per
// batch.
func (f *Platform) FetchProgramStats(_ context.Context, handles []string, save platform.StatsSaver) error {
	f.mu.Lock()
	f.requests++
	f.statsAsks = append(f.statsAsks, append([]string(nil), handles...))
	failErr := f.statsErr
	stats := make([]map[string]platform.ProgramStats, len(handles))
	for i, h := range handles {
		stats[i] = map[string]platform.ProgramStats{}
		if s, ok := f.stats[h]; ok {
			stats[i][h] = s
		}
	}
	f.mu.Unlock()
	for i, h := range handles {
		if failErr != nil && i > 0 {
			return failErr
		}
		if err := save([]string{h}, stats[i]); err != nil {
			return err
		}
	}
	return failErr
}

// Web is the fake with a browser sync (platform.WebSyncer) set up.
type Web struct{ *Platform }

// SetWebStats sets one program's logged-in stats, which only WebSync
// returns.
func (f *Platform) SetWebStats(handle string, s platform.ProgramStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.webStats == nil {
		f.webStats = map[string]platform.ProgramStats{}
	}
	f.webStats[handle] = s
}

// WebRequests returns the handles of each WebSync call.
func (f *Platform) WebRequests() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.webAsks...)
}

// WebSyncConfigured implements platform.WebSyncer.
func (Web) WebSyncConfigured() bool { return true }

// WebSync implements platform.WebSyncer, in one batch.
func (w Web) WebSync(_ context.Context, handles []string, save platform.StatsSaver) error {
	w.mu.Lock()
	w.requests++
	w.webAsks = append(w.webAsks, append([]string(nil), handles...))
	out := map[string]platform.ProgramStats{}
	for _, h := range handles {
		if s, ok := w.webStats[h]; ok {
			out[h] = s
		}
	}
	w.mu.Unlock()
	return save(handles, out)
}

// WebSession implements platform.WebSyncer.
func (Web) WebSession() platform.WebSession { return platform.WebSession{SignedIn: true} }

// WebSyncPulls implements platform.WebSyncer.
func (Web) WebSyncPulls() []string { return []string{"Logged-in stats"} }

// Stats implements platform.Platform.
func (f *Platform) Stats() (int64, int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, 0
}
