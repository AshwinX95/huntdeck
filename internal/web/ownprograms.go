package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Programs that aren't on a supported platform, such as ones companies run
// themselves, are added and edited by hand (platform "independent"). A
// save is a poll of every such program with the edit applied
// (Deps.SaveOwn), so it shows on the program's Changes tab like a
// platform's news would, and a program left out is removed. Saves take
// turns (server.ownMu): two at once would each leave out the other's.

// ownForm is the add/edit form of a program added by hand.
type ownForm struct {
	Handle     string // "" when adding
	Name       string
	URL        string
	Paying     bool
	Currency   string
	BountyMin  string
	BountyMax  string
	Private    bool
	State      string
	InScope    string
	OutScope   string
	Policy     string
	InSeeds    bool
	Error      string
	ReadOnly   bool // a read-only copy shows the form but can't save it
	Currencies []string
}

// ownCurrencies are the currencies the form offers.
var ownCurrencies = []string{"USD", "EUR", "GBP", "CHF", "CAD", "AUD", "JPY", "INR", "SGD"}

// Title is the form's heading.
func (f ownForm) Title() string {
	if f.Handle == "" {
		return "Add a program"
	}
	return "Edit " + f.Name
}

// Action is where the form posts to.
func (f ownForm) Action() string {
	if f.Handle == "" {
		return "/programs/independent/new"
	}
	return "/programs/independent/" + url.PathEscape(f.Handle) + "/edit"
}

// Back is the page the form came from.
func (f ownForm) Back() string {
	if f.Handle == "" {
		return "/programs?platform=" + store.Independent
	}
	return "/programs/independent/" + url.PathEscape(f.Handle)
}

func (s *server) renderOwn(w http.ResponseWriter, r *http.Request, status int, f ownForm) {
	f.ReadOnly, f.Currencies = s.SaveOwn == nil, ownCurrencies
	s.render(w, r, status, "ownprogram", pageData{Title: f.Title(), Nav: "programs", Data: f, FlashErr: f.Error})
}

// newOwnProgram shows an empty form.
func (s *server) newOwnProgram(w http.ResponseWriter, r *http.Request) {
	s.renderOwn(w, r, http.StatusOK, ownForm{Paying: true, Currency: "USD", State: platform.StateOpen})
}

// editOwnProgram shows a program's form.
func (s *server) editOwnProgram(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownFromPath(w, r)
	if !ok {
		return
	}
	p := o.Program
	f := ownForm{Handle: p.Handle, Name: p.Name, URL: p.URL, Paying: p.OffersBounty, Currency: p.Currency, Private: p.Private,
		State: p.State, Policy: p.Policy, InSeeds: o.InSeeds, BountyMin: amountText(p.BountyMin), BountyMax: amountText(p.BountyMax)}
	f.InScope, f.OutScope = independent.ScopeText(o.Scope)
	s.renderOwn(w, r, http.StatusOK, f)
}

func amountText(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// ownFromPath is the live program added by hand named in the path.
func (s *server) ownFromPath(w http.ResponseWriter, r *http.Request) (store.OwnProgram, bool) {
	own, err := s.Store.OwnPrograms(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return store.OwnProgram{}, false
	}
	if i := slices.IndexFunc(own, func(o store.OwnProgram) bool { return o.Program.Handle == r.PathValue("handle") }); i >= 0 {
		return own[i], true
	}
	s.renderError(w, r, http.StatusNotFound, "No such program.")
	return store.OwnProgram{}, false
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// readOwnForm reads the form, with what's wrong with it, if anything.
func readOwnForm(form url.Values) (ownForm, platform.Program, []platform.ScopeEntry, string) {
	f := ownForm{Name: strings.Join(strings.Fields(form.Get("name")), " "), URL: strings.TrimSpace(form.Get("url")),
		Paying: isTrue(form.Get("paying")), Currency: strings.ToUpper(strings.TrimSpace(form.Get("currency"))),
		BountyMin: strings.TrimSpace(form.Get("bounty_min")), BountyMax: strings.TrimSpace(form.Get("bounty_max")),
		Private: isTrue(form.Get("private")), State: oneOf(form.Get("state"), platform.StateOpen, platform.StatePaused, platform.StateClosed),
		InScope: form.Get("in_scope"), OutScope: form.Get("out_scope"), Policy: strings.TrimSpace(form.Get("policy")),
		InSeeds: isTrue(form.Get("in_seeds"))}
	p := platform.Program{Name: f.Name, URL: f.URL, Private: f.Private, State: f.State, OffersBounty: f.Paying, Policy: f.Policy}
	problem := ""
	switch {
	case f.Name == "" || len(f.Name) > 120:
		problem = "Give the program a name of up to 120 characters."
	case len(f.URL) > 500 || !independent.ValidURL(f.URL):
		problem = "The program's page should be a web address starting with https://."
	case len(f.Policy) > 100_000:
		problem = "Rules and notes can be up to 100,000 characters."
	}
	if f.Paying {
		if f.Currency == "" {
			f.Currency = "USD"
		}
		if !currencyCode.MatchString(f.Currency) && problem == "" {
			problem = "Currency should be a three-letter code like USD."
		}
		p.Currency = f.Currency
		lo, err1 := amount(f.BountyMin)
		hi, err2 := amount(f.BountyMax)
		switch {
		case problem != "":
		case err1 != nil || err2 != nil:
			problem = "Bounties should be amounts like 500 or 25000."
		case lo != nil && hi != nil && *lo > *hi:
			problem = "The lowest bounty is more than the highest."
		}
		p.BountyMin, p.BountyMax = lo, hi
	}
	entries, err := independent.ParseScope(f.InScope, f.OutScope, f.Paying)
	if err != nil && problem == "" {
		problem = "Scope: " + err.Error() + "."
	}
	return f, p, entries, problem
}

// amount reads an optional bounty amount.
func amount(v string) (*float64, error) {
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
	if err != nil || n < 0 || n > 1e8 {
		return nil, errors.New("not an amount")
	}
	return &n, nil
}

// saveOwnProgram adds a program (no handle in the path) or saves one.
func (s *server) saveOwnProgram(w http.ResponseWriter, r *http.Request) {
	if s.SaveOwn == nil {
		s.renderError(w, r, http.StatusForbidden, "Programs can't be changed here.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	handle := r.PathValue("handle")
	f, p, entries, problem := readOwnForm(r.PostForm)
	f.Handle = handle
	if problem != "" {
		f.Error = problem
		s.renderOwn(w, r, http.StatusBadRequest, f)
		return
	}
	s.ownMu.Lock()
	defer s.ownMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute) // a save half done when the tab closes is still done
	defer cancel()
	own, err := s.Store.OwnPrograms(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if handle == "" {
		ids, err := s.Store.ProgramIDs(ctx, store.Independent) // removed ones too: their handles stay theirs
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		handle = independent.Slug(f.Name, func(h string) bool { _, ok := ids[h]; return ok })
	} else if !slices.ContainsFunc(own, func(o store.OwnProgram) bool { return o.Program.Handle == handle }) {
		s.renderError(w, r, http.StatusNotFound, "No such program.")
		return
	}
	p.Handle = handle
	listing := listingOf(own, handle, &independent.Program{Program: p, Scope: entries})
	if !s.saveListing(w, r, ctx, listing) {
		return
	}
	ids, err := s.Store.ProgramIDs(ctx, store.Independent)
	if err == nil {
		err = s.Store.SetOwnProgramExtras(ctx, ids[handle], f.InSeeds, f.Policy)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/programs/independent/"+url.PathEscape(handle)+"?msg=program-saved", http.StatusSeeOther)
}

// removeOwnProgram takes a program out: it's marked removed, keeping its
// history, as a platform's would be.
func (s *server) removeOwnProgram(w http.ResponseWriter, r *http.Request) {
	if s.SaveOwn == nil {
		s.renderError(w, r, http.StatusForbidden, "Programs can't be changed here.")
		return
	}
	s.ownMu.Lock()
	defer s.ownMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	own, err := s.Store.OwnPrograms(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	handle := r.PathValue("handle")
	if !slices.ContainsFunc(own, func(o store.OwnProgram) bool { return o.Program.Handle == handle }) {
		s.renderError(w, r, http.StatusNotFound, "No such program.")
		return
	}
	if !s.saveListing(w, r, ctx, listingOf(own, handle, nil)) {
		return
	}
	http.Redirect(w, r, "/programs?platform="+store.Independent+"&msg=program-removed", http.StatusSeeOther)
}

// listingOf is the stored programs with the one at handle replaced by p
// (added, if none is), or left out when p is nil.
func listingOf(own []store.OwnProgram, handle string, p *independent.Program) []independent.Program {
	out := make([]independent.Program, 0, len(own)+1)
	for _, o := range own {
		if o.Program.Handle != handle {
			out = append(out, independent.Program{Program: o.Program, Scope: o.Scope})
		}
	}
	if p != nil {
		out = append(out, *p)
	}
	return out
}

// saveListing saves the programs added by hand; it writes the response
// itself when it returns false.
func (s *server) saveListing(w http.ResponseWriter, r *http.Request, ctx context.Context, listing []independent.Program) bool { //nolint:revive // ctx outlives r's
	err := s.SaveOwn(ctx, listing)
	if errors.Is(err, store.ErrRunInProgress) {
		s.renderError(w, r, http.StatusConflict, "Another save is still running. Try again in a moment.")
		return false
	}
	if err != nil {
		s.serverError(w, r, err)
		return false
	}
	return true
}

// listPlatforms are the platforms a list can be filtered by: the
// configured ones, and Independent once it has programs.
func (s *server) listPlatforms(ctx context.Context) []string {
	names := s.platformNames()
	if ok, err := s.Store.HasPrograms(ctx, store.Independent); err == nil && ok && !slices.Contains(names, store.Independent) {
		names = append(names, store.Independent)
	}
	return names
}

// The Overview of a program added by hand has no stats to show, so it
// shows what was entered: the scope at a glance, the rules and notes, and
// the program's facts (templates/partials/program-overview.html).

// ownScopePreview is how many in-scope assets the Overview lists.
const ownScopePreview = 12

// ScopePreview is the first in-scope assets, in the Scope tab's order.
func (d programData) ScopePreview() []store.EntryView {
	return d.InScope[:min(len(d.InScope), ownScopePreview)]
}

// Rules is the program's rules and notes, nil if there are none.
func (d programData) Rules() *policyDoc {
	for i := range d.Docs {
		if d.Docs[i].ID == "policy" {
			return &d.Docs[i]
		}
	}
	return nil
}
