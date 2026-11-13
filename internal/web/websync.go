package web

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/store"
)

// webSyncRow is one platform's browser sync on the settings page.
type webSyncRow struct {
	Name string
	store.WebSyncSettings
	Overridden bool
	Running    bool // the platform is enabled, so the sync can run
	Session    platform.WebSession
	Pulls      []string
	Last       *store.Run
}

// PullsText is what the sync reads for each program, in a line.
func (w webSyncRow) PullsText() string { return strings.Join(w.Pulls, "; ") }

// webSyncRows lists the platforms with a browser sync, by name.
func (s *server) webSyncRows(latest []store.Run) []webSyncRow {
	var out []webSyncRow
	for _, name := range s.platformNames() {
		ws, ok := s.WebSyncs[name]
		if !ok {
			continue
		}
		row := webSyncRow{Name: name, WebSyncSettings: store.DefaultWebSync, Session: ws.WebSession(), Pulls: ws.WebSyncPulls()}
		if s.Schedule != nil {
			row.WebSyncSettings, row.Overridden = s.Schedule.Web(name), s.Schedule.WebOverridden(name)
		}
		row.Running = s.platformSettings(name).Enabled
		if i := slices.IndexFunc(latest, func(r store.Run) bool { return r.Platform == name && r.Mode == "web" }); i >= 0 {
			row.Last = &latest[i]
		}
		out = append(out, row)
	}
	return out
}

// saveWebSync saves one platform's browser sync switch and interval.
func (s *server) saveWebSync(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if s.Schedule == nil {
		s.renderSettings(w, r, http.StatusServiceUnavailable, "Settings can't be changed in this process.")
		return
	}
	name := canonicalPlatform(r.PostFormValue("platform"))
	if _, ok := s.WebSyncs[name]; !ok {
		s.renderSettings(w, r, http.StatusNotFound, "That platform has no browser sync set up.")
		return
	}
	ws, err := s.webSyncFrom(name, r.PostFormValue("enabled"), r.PostFormValue("interval"))
	if err == nil {
		err = s.Schedule.SetWeb(r.Context(), name, ws)
	}
	var invalid scheduler.ValidationError
	switch {
	case err == nil:
		http.Redirect(w, r, "/settings?msg=websync-saved#websync", http.StatusSeeOther)
	case errors.Is(err, scheduler.ErrUnknownPlatform):
		s.renderSettings(w, r, http.StatusNotFound, "That platform isn't configured.")
	case errors.As(err, &invalid):
		s.renderSettings(w, r, http.StatusBadRequest, platformLabel(name)+": "+invalid.Error()+".")
	default:
		s.serverError(w, r, err)
	}
}

// webSyncFrom reads a browser sync's switch and interval as a form gives
// them; "off" switches it off, keeping the interval.
func (s *server) webSyncFrom(name, enabled, interval string) (store.WebSyncSettings, error) {
	ws := store.WebSyncSettings{Enabled: isTrue(enabled)}
	var err error
	if ws.Interval, err = scheduler.ParseInterval(interval); err == nil && ws.Interval == 0 {
		ws.Enabled, ws.Interval = false, s.Schedule.Web(name).Interval
	}
	return ws, err
}

// resetWebSync drops a platform's saved browser sync settings.
func (s *server) resetWebSync(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if s.Schedule == nil {
		s.renderSettings(w, r, http.StatusServiceUnavailable, "Settings can't be changed in this process.")
		return
	}
	err := s.Schedule.ResetWeb(r.Context(), canonicalPlatform(r.PostFormValue("platform")))
	switch {
	case errors.Is(err, scheduler.ErrUnknownPlatform):
		s.renderSettings(w, r, http.StatusNotFound, "That platform isn't configured.")
	case err != nil:
		s.serverError(w, r, err)
	default:
		http.Redirect(w, r, "/settings?msg=websync-reset#websync", http.StatusSeeOther)
	}
}
