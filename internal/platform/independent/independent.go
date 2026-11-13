// Package independent is the "platform" of programs that aren't on a
// supported platform: ones companies run themselves, added and edited by
// hand. It makes no requests. The web UI builds the whole listing from what
// is stored, with the user's edit applied, and polls it like any platform,
// so the sync engine records the edit as changes (scope added, removed and
// edited; policy edits as diffs), derives targets and marks removals.
//
// Like other platform packages it never touches the database: a Source is
// only the listing it was given.
package independent

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// Name is the platform name programs added by hand are stored under.
const Name = "independent"

// Program is one program as its form has it, with its scope.
type Program struct {
	platform.Program
	Scope []platform.ScopeEntry
}

// Source lists the programs it was given: always the complete listing,
// so a program left out of it is removed.
type Source struct {
	programs []Program
}

// New returns a Source listing programs.
func New(programs []Program) *Source { return &Source{programs: programs} }

// Name implements platform.Platform.
func (*Source) Name() string { return Name }

// ListPrograms implements platform.Platform.
func (s *Source) ListPrograms(context.Context) (platform.Listing, error) {
	l := platform.Listing{Complete: true, Programs: make([]platform.Program, 0, len(s.programs))}
	for _, p := range s.programs {
		l.Programs = append(l.Programs, p.Program)
	}
	return l, nil
}

// FetchScope implements platform.Platform.
func (s *Source) FetchScope(_ context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	for _, q := range s.programs {
		if q.Handle == p.Handle {
			return q.Scope, nil
		}
	}
	return nil, fmt.Errorf("independent: no program %q", p.Handle)
}

// Stats implements platform.Platform: it makes no requests.
func (*Source) Stats() (requests, rateLimited int64) { return 0, 0 }

// Limits on what a form may hold.
const (
	MaxScopeLines = 2000
	MaxLineLength = 500
)

// ParseScope reads the form's scope boxes, one asset per line; blank lines
// and repeats are skipped. Entries on paying programs are bounty-eligible
// when in scope.
func ParseScope(in, out string, paying bool) ([]platform.ScopeEntry, error) {
	var entries []platform.ScopeEntry
	seen := map[string]bool{}
	for _, side := range []struct {
		text string
		in   bool
	}{{in, true}, {out, false}} {
		for line := range strings.SplitSeq(side.text, "\n") {
			ident := strings.TrimSpace(line)
			if ident == "" {
				continue
			}
			if len(ident) > MaxLineLength {
				return nil, fmt.Errorf("scope lines can be up to %d characters", MaxLineLength)
			}
			key := fmt.Sprint(side.in, strings.ToLower(ident))
			if seen[key] {
				continue
			}
			seen[key] = true
			if len(entries) == MaxScopeLines {
				return nil, fmt.Errorf("up to %d scope lines", MaxScopeLines)
			}
			kind := AssetType(ident)
			entries = append(entries, platform.ScopeEntry{InScope: side.in, AssetType: kind, PlatformAssetType: kind,
				Identifier: ident, BountyEligible: side.in && paying})
		}
	}
	return entries, nil
}

// AssetType classifies a typed scope line: an IP address or CIDR range,
// else a wildcard, URL, domain or "other" (scope.WebType).
func AssetType(ident string) string {
	if _, _, err := net.ParseCIDR(ident); err == nil {
		return scope.AssetCIDR
	}
	if net.ParseIP(ident) != nil {
		return scope.AssetIP
	}
	return scope.WebType(ident)
}

// ScopeText is the inverse of ParseScope, for the edit form: the in-scope
// and out-of-scope boxes.
func ScopeText(entries []platform.ScopeEntry) (in, out string) {
	var ins, outs []string
	for _, e := range entries {
		if e.InScope {
			ins = append(ins, e.Identifier)
		} else {
			outs = append(outs, e.Identifier)
		}
	}
	return strings.Join(ins, "\n"), strings.Join(outs, "\n")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is a program's handle made from its name: "Meta Bug Bounty" is
// "meta-bug-bounty". Taken says which handles are in use; a name whose
// slug is taken gets "-2", "-3" and so on. "new" is kept for the add form.
func Slug(name string, taken func(string) bool) string {
	base := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) > 60 {
		base = strings.TrimRight(base[:60], "-")
	}
	if base == "" {
		base = "program"
	}
	handle := base
	for n := 2; handle == "new" || taken(handle); n++ {
		handle = fmt.Sprintf("%s-%d", base, n)
	}
	return handle
}

// ValidURL reports whether u is an absolute http(s) address, as a
// program's page must be.
func ValidURL(u string) bool {
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != "" && !strings.ContainsAny(u, " \t\n")
}
