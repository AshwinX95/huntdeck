package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// Settings are one platform's polling settings.
type Settings struct {
	Enabled bool
	Quick   time.Duration // 0: no scheduled quick polls
	Full    time.Duration // 0: no scheduled full polls
}

// Interval is how often mode runs, or 0 if it doesn't (including when the
// platform is disabled).
func (s Settings) Interval(mode string) time.Duration {
	switch {
	case !s.Enabled:
		return 0
	case mode == syncer.ModeFull:
		return s.Full
	default:
		return s.Quick
	}
}

// ValidationError explains why settings can't be saved, in words fit for
// the UI.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

// Limits keep edited intervals polite to platforms.
const (
	MinQuick    = 5 * time.Minute
	MinFull     = time.Hour
	MaxInterval = 7 * 24 * time.Hour
)

// Validate reports why s can't be saved, in words fit for the UI.
func (s Settings) Validate() error {
	switch {
	case s.Quick != 0 && (s.Quick < MinQuick || s.Quick > MaxInterval):
		return ValidationError("quick poll interval must be off or between " + FormatInterval(MinQuick) + " and 7 days")
	case s.Full != 0 && (s.Full < MinFull || s.Full > MaxInterval):
		return ValidationError("full poll interval must be off or between " + FormatInterval(MinFull) + " and 7 days")
	case s.Quick != 0 && s.Full != 0 && s.Quick >= s.Full:
		return ValidationError("the quick poll must run more often than the full poll")
	}
	return nil
}

// ParseInterval reads an interval typed in the UI: "15m", "6h", "1d",
// "1h30m", or "off"/"0"/"" for none.
func ParseInterval(v string) (time.Duration, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "0", "off", "none", "never":
		return 0, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, ValidationError(strconv.Quote(v) + " isn't an interval; use something like 15m, 6h or 1d")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, ValidationError(strconv.Quote(v) + " isn't an interval; use something like 15m, 6h or 1d")
	}
	return d, nil
}

// FormatInterval writes d the way ParseInterval reads it.
func FormatInterval(d time.Duration) string {
	switch {
	case d == 0:
		return "off"
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0 && d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	out := d.String() // e.g. "1h30m0s" or "2h0m30s"
	if strings.HasSuffix(out, "m0s") {
		out = strings.TrimSuffix(out, "0s")
	}
	return out
}

// Schedule holds every platform's polling settings: defaults from the
// environment, overridden by settings saved in the UI. Saving wakes the
// scheduler, so changes apply without a restart.
type Schedule struct {
	st       *store.Store
	defaults map[string]Settings

	mu       sync.Mutex
	override map[string]Settings
	web      map[string]store.WebSyncSettings // saved browser sync settings
	changed  chan struct{}
}

const settingPrefix = "platform."

type savedSettings struct {
	Enabled bool   `json:"enabled"`
	Quick   string `json:"quick"`
	Full    string `json:"full"`
}

// NewSchedule loads saved overrides for the platforms in defaults.
// Overrides that no longer parse are ignored, falling back to the default.
func NewSchedule(ctx context.Context, st *store.Store, defaults map[string]Settings) (*Schedule, error) {
	s := &Schedule{st: st, defaults: defaults, override: map[string]Settings{}, web: map[string]store.WebSyncSettings{},
		changed: make(chan struct{})}
	for name := range defaults {
		ws, saved, err := st.WebSync(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("load browser sync settings: %w", err)
		}
		if saved && validWebInterval(ws.Interval) {
			s.web[name] = ws
		}
	}
	saved, err := st.Settings(ctx, settingPrefix)
	if err != nil {
		return nil, fmt.Errorf("load schedule settings: %w", err)
	}
	for key, raw := range saved {
		name := strings.TrimPrefix(key, settingPrefix)
		if _, ok := defaults[name]; !ok {
			continue
		}
		var v savedSettings
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		q, qerr := ParseInterval(v.Quick)
		f, ferr := ParseInterval(v.Full)
		set := Settings{Enabled: v.Enabled, Quick: q, Full: f}
		if qerr != nil || ferr != nil || set.Validate() != nil {
			continue
		}
		s.override[name] = set
	}
	return s, nil
}

// Get returns a platform's current settings.
func (s *Schedule) Get(platform string) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.override[platform]; ok {
		return o
	}
	return s.defaults[platform]
}

// Default returns a platform's settings from the environment.
func (s *Schedule) Default(platform string) Settings { return s.defaults[platform] }

// Overridden reports whether a platform uses settings saved in the UI.
func (s *Schedule) Overridden(platform string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.override[platform]
	return ok
}

// Enabled reports whether a platform may be polled and logged in to.
func (s *Schedule) Enabled(platform string) bool { return s.Get(platform).Enabled }

// ErrUnknownPlatform is returned for platforms that aren't configured.
var ErrUnknownPlatform = errors.New("platform not configured")

// Set saves a platform's settings and applies them immediately.
func (s *Schedule) Set(ctx context.Context, platform string, set Settings) error {
	if _, ok := s.defaults[platform]; !ok {
		return ErrUnknownPlatform
	}
	if err := set.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(savedSettings{set.Enabled, FormatInterval(set.Quick), FormatInterval(set.Full)})
	if err != nil {
		return err
	}
	if err := s.st.Write(ctx, func(tx *store.Tx) error { return tx.PutSetting(ctx, settingPrefix+platform, string(raw)) }); err != nil {
		return err
	}
	s.mu.Lock()
	s.override[platform] = set
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

// Reset drops a platform's saved settings, going back to the defaults.
func (s *Schedule) Reset(ctx context.Context, platform string) error {
	if _, ok := s.defaults[platform]; !ok {
		return ErrUnknownPlatform
	}
	if err := s.st.Write(ctx, func(tx *store.Tx) error { return tx.DeleteSetting(ctx, settingPrefix+platform) }); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.override, platform)
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

// Browser sync limits (see platform.WebSyncer): a few hundred requests a run.
const (
	MinWeb = 6 * time.Hour
)

func validWebInterval(d time.Duration) bool { return d >= MinWeb && d <= MaxInterval }

// Web returns a platform's browser sync settings.
func (s *Schedule) Web(platform string) store.WebSyncSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws, ok := s.web[platform]; ok {
		return ws
	}
	return store.DefaultWebSync
}

// WebOverridden reports whether a platform's browser sync uses settings
// saved in the UI.
func (s *Schedule) WebOverridden(platform string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.web[platform]
	return ok
}

// WebInterval is how often a platform's browser sync runs, or 0 if it
// doesn't (it's off, or the platform is).
func (s *Schedule) WebInterval(platform string) time.Duration {
	ws := s.Web(platform)
	if !ws.Enabled || !s.Enabled(platform) {
		return 0
	}
	return ws.Interval
}

// SetWeb saves a platform's browser sync settings and applies them
// immediately.
func (s *Schedule) SetWeb(ctx context.Context, platform string, ws store.WebSyncSettings) error {
	if _, ok := s.defaults[platform]; !ok {
		return ErrUnknownPlatform
	}
	if !validWebInterval(ws.Interval) {
		return ValidationError("browser sync interval must be between " + FormatInterval(MinWeb) + " and 7 days")
	}
	if err := s.st.SaveWebSync(ctx, platform, ws); err != nil {
		return err
	}
	s.mu.Lock()
	s.web[platform] = ws
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

// ResetWeb drops a platform's saved browser sync settings.
func (s *Schedule) ResetWeb(ctx context.Context, platform string) error {
	if _, ok := s.defaults[platform]; !ok {
		return ErrUnknownPlatform
	}
	if err := s.st.ResetWebSync(ctx, platform); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.web, platform)
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

// Changed returns a channel that is closed at the next change.
func (s *Schedule) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func (s *Schedule) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}
