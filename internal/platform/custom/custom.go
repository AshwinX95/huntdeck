// Package custom is a platform HuntDeck has no client for, such as
// Inspectiv: a "custom source". A script of the user's reads the platform
// and writes what it found as one JSON document in HuntDeck's own format
// (below; docs/custom-sources.md walks through it), at a file path or an
// http(s) address set in HUNTDECK_SOURCES. HuntDeck polls it like any
// platform: programs and scope are stored, changes recorded, targets
// derived, and programs a complete listing leaves out marked removed.
//
// Request budget: one read of the document per poll (a file, or one GET
// through the shared rate-limited client); scope comes from the document
// that poll read, with no further requests. Only the configured address
// is ever contacted.
//
// Like other platform packages it never touches the database.
package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// Limits on a document.
const (
	MaxBytes    = 32 << 20
	MaxPrograms = 10_000
	MaxScope    = 5_000 // entries per program
)

// Version is the format's version: a document says which it's written in.
const Version = 1

// Document is a custom source's listing, as its JSON has it.
type Document struct {
	Version int `json:"version"`
	// Complete says the script read every program the account can see:
	// only then are programs left out marked removed. Omitted, it's true;
	// a script that couldn't read everything sets it false.
	Complete *bool        `json:"complete,omitempty"`
	Programs []DocProgram `json:"programs"`
}

// DocProgram is one program of a Document.
type DocProgram struct {
	Handle       string     `json:"handle"` // stable, as in the platform's URLs: letters, digits, . _ -
	Name         string     `json:"name"`
	URL          string     `json:"url"`             // the program's page, http(s)
	State        string     `json:"state,omitempty"` // open (the default), paused or closed
	Private      bool       `json:"private,omitempty"`
	OffersBounty bool       `json:"offers_bounty,omitempty"`
	Currency     string     `json:"currency,omitempty"` // three letters; USD if it pays and doesn't say
	BountyMin    *float64   `json:"bounty_min,omitempty"`
	BountyMax    *float64   `json:"bounty_max,omitempty"`
	Policy       string     `json:"policy,omitempty"` // rules, Markdown
	StartedAt    *time.Time `json:"launched_at,omitempty"`
	Scope        []DocEntry `json:"scope"`
}

// DocEntry is one scope entry of a DocProgram.
type DocEntry struct {
	ID         string `json:"id,omitempty"` // the platform's, if it has one: edits then stay edits
	Identifier string `json:"identifier"`
	// Type is one of HuntDeck's asset types (wildcard, domain, url, ip,
	// cidr, android, ios, …); left out or unknown, it's worked out from the
	// identifier, and kept as the platform's own type.
	Type    string `json:"type,omitempty"`
	InScope *bool  `json:"in_scope,omitempty"` // default true
	// Bounty says the entry is bounty-eligible; default: in scope on a
	// program that pays.
	Bounty      *bool  `json:"bounty,omitempty"`
	MaxSeverity string `json:"max_severity,omitempty"`
	Instruction string `json:"instruction,omitempty"`
}

// Options configure a Source.
type Options struct {
	Name     string // the platform name it's stored under
	Location string // an absolute file path, or an http(s) address
	HTTP     *httpclient.Client
}

// Source reads one custom source.
type Source struct {
	name, location string
	http           *httpclient.Client

	mu    sync.Mutex
	scope map[string][]platform.ScopeEntry // by handle, from the last listing
}

// New returns a Source.
func New(o Options) (*Source, error) {
	if o.Name == "" || o.Location == "" {
		return nil, errors.New("custom: a source needs a name and a location")
	}
	if !filepath.IsAbs(o.Location) && o.HTTP == nil {
		return nil, fmt.Errorf("custom %s: an address needs an HTTP client", o.Name)
	}
	return &Source{name: o.Name, location: o.Location, http: o.HTTP}, nil
}

// Name implements platform.Platform.
func (s *Source) Name() string { return s.name }

// Stats implements platform.Platform.
func (s *Source) Stats() (requests, rateLimited int64) {
	if s.http == nil {
		return 0, 0
	}
	return s.http.Stats()
}

// ListPrograms implements platform.Platform: it reads the document, and
// keeps its scope for FetchScope.
func (s *Source) ListPrograms(ctx context.Context) (platform.Listing, error) {
	raw, err := s.read(ctx)
	if err != nil {
		return platform.Listing{}, err
	}
	doc, err := Parse(raw)
	if err != nil {
		return platform.Listing{}, fmt.Errorf("custom %s: %w", s.name, err)
	}
	l := platform.Listing{Complete: doc.Complete == nil || *doc.Complete}
	scopes := make(map[string][]platform.ScopeEntry, len(doc.Programs))
	for _, p := range doc.Programs {
		prog, entries := p.platform()
		l.Programs = append(l.Programs, prog)
		scopes[prog.Handle] = entries
	}
	s.mu.Lock()
	s.scope = scopes
	s.mu.Unlock()
	return l, nil
}

// FetchScope implements platform.Platform, from the document the listing
// read.
func (s *Source) FetchScope(_ context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, ok := s.scope[p.Handle]
	if !ok {
		return nil, fmt.Errorf("custom %s: no program %q in the document", s.name, p.Handle)
	}
	return entries, nil
}

// read is the document's bytes, from the file or the address.
func (s *Source) read(ctx context.Context) ([]byte, error) {
	if filepath.IsAbs(s.location) {
		f, err := os.Open(s.location)
		if err != nil {
			return nil, fmt.Errorf("custom %s: %w", s.name, err)
		}
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
		if err == nil && len(b) > MaxBytes {
			err = fmt.Errorf("over %d MB", MaxBytes>>20)
		}
		if err != nil {
			return nil, fmt.Errorf("custom %s: read %s: %w", s.name, filepath.Base(s.location), err)
		}
		return b, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.location, nil)
	if err != nil {
		return nil, fmt.Errorf("custom %s: %w", s.name, err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.http.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("custom %s: fetch the document: %w", s.name, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("custom %s: fetch the document: HTTP %d", s.name, res.StatusCode)
	}
	if len(res.Body) > MaxBytes {
		return nil, fmt.Errorf("custom %s: the document is over %d MB", s.name, MaxBytes>>20)
	}
	return res.Body, nil
}

var (
	handlePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	// assetTypes are the types a document may give an entry.
	assetTypes = []string{scope.AssetWildcard, scope.AssetDomain, scope.AssetURL, scope.AssetAPI, scope.AssetIP, scope.AssetCIDR,
		scope.AssetAndroid, scope.AssetIOS, scope.AssetWindows, scope.AssetSourceCode, scope.AssetExecutable, scope.AssetHardware,
		scope.AssetSmartContract, scope.AssetAIModel, scope.AssetOther}
)

// Parse reads and checks a document. Any mistake fails it whole, naming
// the program: a listing half read would look like removals.
func Parse(raw []byte) (Document, error) {
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("not JSON in the custom source format: %w", err)
	}
	switch {
	case doc.Version != Version:
		return doc, fmt.Errorf("version %d; this HuntDeck reads version %d", doc.Version, Version)
	case doc.Programs == nil:
		return doc, errors.New(`no "programs" list`)
	case len(doc.Programs) > MaxPrograms:
		return doc, fmt.Errorf("over %d programs", MaxPrograms)
	}
	seen := map[string]bool{}
	for i, p := range doc.Programs {
		what := fmt.Sprintf("program %d (%q)", i+1, p.Handle)
		switch {
		case !handlePattern.MatchString(p.Handle):
			return doc, fmt.Errorf("%s: a handle is up to 100 letters, digits, dots, dashes and underscores", what)
		case seen[p.Handle]:
			return doc, fmt.Errorf("%s: handle listed twice", what)
		case strings.TrimSpace(p.Name) == "":
			return doc, fmt.Errorf("%s: no name", what)
		case !independent.ValidURL(p.URL):
			return doc, fmt.Errorf("%s: url should be the program's page, http(s)", what)
		case p.State != "" && !slices.Contains([]string{platform.StateOpen, platform.StatePaused, platform.StateClosed}, p.State):
			return doc, fmt.Errorf("%s: state should be open, paused or closed", what)
		case p.Currency != "" && !currencyPattern.MatchString(p.Currency):
			return doc, fmt.Errorf("%s: currency should be three capital letters, like USD", what)
		case p.BountyMin != nil && p.BountyMax != nil && *p.BountyMin > *p.BountyMax:
			return doc, fmt.Errorf("%s: bounty_min is more than bounty_max", what)
		case len(p.Scope) > MaxScope:
			return doc, fmt.Errorf("%s: over %d scope entries", what, MaxScope)
		}
		seen[p.Handle] = true
		for j, e := range p.Scope {
			if strings.TrimSpace(e.Identifier) == "" || len(e.Identifier) > independent.MaxLineLength {
				return doc, fmt.Errorf("%s, scope entry %d: an identifier is 1 to %d characters", what, j+1, independent.MaxLineLength)
			}
		}
	}
	return doc, nil
}

// platform is the program and its scope as the sync engine takes them.
func (p DocProgram) platform() (platform.Program, []platform.ScopeEntry) {
	prog := platform.Program{Handle: p.Handle, Name: strings.Join(strings.Fields(p.Name), " "), URL: p.URL, Private: p.Private,
		State: p.State, OffersBounty: p.OffersBounty, Policy: p.Policy, StartedAt: p.StartedAt}
	if prog.State == "" {
		prog.State = platform.StateOpen
	}
	if p.OffersBounty {
		prog.Currency = p.Currency
		if prog.Currency == "" {
			prog.Currency = "USD"
		}
		prog.BountyMin, prog.BountyMax = p.BountyMin, p.BountyMax
	}
	entries := make([]platform.ScopeEntry, 0, len(p.Scope))
	for _, e := range p.Scope {
		ident := strings.TrimSpace(e.Identifier)
		in := e.InScope == nil || *e.InScope
		kind, raw := strings.ToLower(e.Type), e.Type
		if !slices.Contains(assetTypes, kind) {
			kind = independent.AssetType(ident)
		}
		if raw == "" {
			raw = kind
		}
		bounty := in && p.OffersBounty
		if e.Bounty != nil {
			bounty = *e.Bounty
		}
		entries = append(entries, platform.ScopeEntry{PlatformID: e.ID, InScope: in, AssetType: kind, PlatformAssetType: raw,
			Identifier: ident, BountyEligible: bounty, MaxSeverity: strings.ToLower(e.MaxSeverity), Instruction: e.Instruction})
	}
	return prog, entries
}
