package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/store"
)

// location is the time zone pages show times in: the one chosen in
// Settings, else Deps.Timezone, else UTC. It's read once and kept until
// Settings changes it. A read-only copy (a demo) is for visitors anywhere:
// it shows UTC, and app.js puts times in the visitor's own zone.
func (s *server) location(ctx context.Context) *time.Location {
	if loc := s.zone.Load(); loc != nil {
		return loc
	}
	if s.ReadOnly {
		s.zone.Store(time.UTC)
		return time.UTC
	}
	loc, err := s.Store.Timezone(ctx, s.Timezone)
	if err != nil {
		s.Log.Warn("time zone setting not read", "err", err)
		return s.shownZone()
	}
	s.zone.Store(loc)
	return loc
}

// shownZone is the zone the template's time funcs use; render loads it
// first.
func (s *server) shownZone() *time.Location {
	if loc := s.zone.Load(); loc != nil {
		return loc
	}
	return time.UTC
}

// now is the current time in the user's time zone.
func (s *server) now(ctx context.Context) time.Time {
	return time.Now().In(s.location(ctx))
}

// zoneForm is the Settings time zone form.
type zoneForm struct {
	Name     string // the zone in use
	Default  string // HUNTDECK_TIMEZONE's
	Overrode bool   // chosen in Settings rather than the default
	Now      string // the time there now, as pages show it
	Choices  []zoneChoice
}

// zoneEntry is one of zoneTable's zones.
type zoneEntry struct{ Name, Country string }

// zoneChoice is one zone in the picker: its name, where it is and its
// offset from UTC now, and what a search matches.
type zoneChoice struct {
	Name, Country string
	Offset        string // "UTC+10:00", "UTC−03:30"; DST as it is now
	Abbrev        string // "AEST" where the zone has a letter abbreviation
	Search        string // lowercased words: name, city, country, offset, abbreviation
	On            bool   // the zone in use
}

// zoneChoices are every zone in zoneTable as it is at now, current
// marking the one in use. A zone in use that isn't in the table (an old
// name such as US/Eastern) comes first, so it shows.
func zoneChoices(now time.Time, current string) []zoneChoice {
	out := make([]zoneChoice, 0, len(zoneTable)+1)
	add := func(e zoneEntry) {
		loc, err := time.LoadLocation(e.Name)
		if err != nil {
			return
		}
		t := now.In(loc)
		abbr, sec := t.Zone()
		sign := "+"
		if sec < 0 {
			sign, sec = "−", -sec
		}
		c := zoneChoice{Name: e.Name, Country: e.Country, On: e.Name == current,
			Offset: fmt.Sprintf("UTC%s%02d:%02d", sign, sec/3600, sec%3600/60)}
		if strings.Trim(abbr, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" && abbr != e.Name {
			c.Abbrev = abbr
		}
		words := strings.NewReplacer("/", " ", "_", " ", "−", "-").Replace(e.Name + " " + e.Country + " " + c.Offset + " " + c.Abbrev)
		c.Search = strings.ToLower(words)
		out = append(out, c)
	}
	if !slices.ContainsFunc(zoneTable, func(e zoneEntry) bool { return e.Name == current }) {
		add(zoneEntry{Name: current})
	}
	for _, e := range zoneTable {
		add(e)
	}
	return out
}

func (s *server) zoneForm(ctx context.Context) (zoneForm, error) {
	set, err := s.Store.Settings(ctx, store.TimezoneSetting)
	if err != nil {
		return zoneForm{}, err
	}
	loc := s.location(ctx)
	def := s.Timezone
	if def == "" {
		def = "UTC"
	}
	now := time.Now()
	return zoneForm{Name: loc.String(), Default: def, Overrode: set[store.TimezoneSetting] != "",
		Now: now.In(loc).Format("2006-01-02 15:04 MST"), Choices: zoneChoices(now, loc.String())}, nil
}

// saveTimezone stores the time zone chosen in Settings, or with reset
// goes back to the default.
func (s *server) saveTimezone(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	name := strings.TrimSpace(r.PostFormValue("timezone"))
	reset := r.PostFormValue("reset") != ""
	if !reset && !config.ValidTimezone(name) {
		s.renderSettings(w, r, http.StatusBadRequest, "Unknown time zone. Use a name like Australia/Brisbane, Europe/London or UTC.")
		return
	}
	err := s.Store.Write(r.Context(), func(tx *store.Tx) error {
		if reset {
			return tx.DeleteSetting(r.Context(), store.TimezoneSetting)
		}
		return tx.PutSetting(r.Context(), store.TimezoneSetting, name)
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.zone.Store(nil) // read again on the next page
	http.Redirect(w, r, "/settings?tab=display&msg=timezone-saved", http.StatusSeeOther)
}
