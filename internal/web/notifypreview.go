package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/notify"
)

// The Notifications page shows what messages look like, and the channel
// form what a channel would have been sent in the last week; both from the
// changes recorded then (notify.PreviewChannel).

// previewWeek is how far back previews look.
const previewWeek = 7 * 24 * time.Hour

// cardAssets is how many assets an example message lists.
const cardAssets = 3

// messageCard is an example message (partials/notify.html's "message").
type messageCard struct {
	notify.Card
	Kind    string
	Tone    string // its bar's colour: good, accent, warn or bad
	At      time.Time
	Program *notify.Program
	Local   string // the program's page here
}

func messageCards(events []notify.Event) []messageCard {
	out := make([]messageCard, 0, len(events))
	for _, e := range events {
		c := messageCard{Card: e.Card(), Kind: e.Kind, Tone: eventTone(e.Kind), At: e.At, Program: e.Program}
		if len(c.Assets) > cardAssets {
			c.More += len(c.Assets) - cardAssets
			c.Assets = c.Assets[:cardAssets]
		}
		if p := e.Program; p != nil {
			c.Local = "/programs/" + url.PathEscape(p.Platform) + "/" + url.PathEscape(p.Handle)
		}
		out = append(out, c)
	}
	return out
}

func eventTone(kind string) string {
	switch kind {
	case notify.EventInvite, notify.EventLaunch, notify.EventReopened, notify.EventPlatformRecovered:
		return "good"
	case notify.EventScopeAdded, notify.EventBountyRaised, notify.EventAnnouncement, notify.EventReportBounty:
		return "accent"
	case notify.EventPlatformProblem:
		return "bad"
	}
	return "warn"
}

// previewView is what a channel would have been sent (partials/notify.html's
// "channel-preview").
type previewView struct {
	notify.Preview
	Cards   []messageCard
	Sends   bool // after each poll or with a digest; neither sends nothing
	Disable bool // the channel is switched off
}

// Rate is how often messages would come, in words.
func (v previewView) Rate() string {
	switch d := v.PerDay(); {
	case v.Messages == 0:
		return ""
	case d >= 1.5:
		return fmt.Sprintf("about %.0f a day", d)
	case d >= .75:
		return "about one a day"
	default:
		return fmt.Sprintf("about %d a week", max(1, int(math.Round(d*7))))
	}
}

// preview works out what rules would have sent over the last week.
func (s *server) preview(ctx context.Context, rules notify.Rules, instant bool, digestAt string, show int) (previewView, error) {
	now := s.Store.Now()
	since := now.Add(-previewWeek).Truncate(time.Hour) // an hour's steps, so the cached changes are reused
	changes, err := s.Store.NotifyChangesSince(ctx, since)
	if err != nil {
		return previewView{}, err
	}
	p := notify.PreviewChannel(changes, rules, instant, digestAt, s.location(ctx), since, now, show)
	return previewView{Preview: p, Cards: messageCards(p.Shown), Sends: instant || digestAt != ""}, nil
}

// channelPreview is the channel form's preview for the choices in the
// query (the form's fields, without its address and secrets), as an HTML
// fragment the form swaps in as they change (static/channel.js).
func (s *server) channelPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rules, _ := s.rulesFrom(q) // a half-typed field previews as if empty
	digestAt, err := notify.ParseDigestAt(strings.TrimSpace(q.Get("digest_at")))
	if err != nil {
		digestAt = ""
	}
	v, err := s.preview(r.Context(), rules, isTrue(q.Get("instant")), digestAt, previewShown)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Disable = !isTrue(q.Get("enabled"))
	s.fragment(w, r, "notification", "channel-preview", v)
}

// previewShown is how many example messages the channel form shows.
const previewShown = 4
