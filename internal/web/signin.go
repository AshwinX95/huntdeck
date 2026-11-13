package web

import (
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Settings' Sign-in column: each credential a platform signs in with, and
// whether the platform accepted it when it was last used.

// signIn is one credential's line.
type signIn struct {
	Name    string     // "API token", "Login", "Website login"
	State   string     // the chip's class: "ok", "failed", or "" not known yet
	Label   string     // the chip's text
	Parts   []signPart // what's known, after the name
	Problem string     // a refusal, in full, under the line
	// Watch links Settings' Browser tab, for website logins when VNC is
	// on: to see a login, or finish one by hand.
	Watch string
}

// Title is the line in words, for its tooltip: "Login: accepted 4m ago".
func (l signIn) Title() string {
	t := l.Name + ": " + l.Label
	for _, p := range l.Parts {
		t += " · " + p.Text
		if p.When != nil {
			t += " " + ago(*p.When)
		}
		if p.In != nil {
			t += " " + until(*p.In) + " (" + p.In.UTC().Format("Jan 2 15:04") + " UTC)"
		}
		if p.At != nil {
			t += " " + p.At.UTC().Format("Jan 2 15:04") + " UTC"
		}
	}
	return t
}

// signPart is "accepted 4m ago": text, then how long ago When, how long
// until In, or the date and time At.
type signPart struct {
	Text         string
	When, In, At *time.Time
}

// signIns are a platform's credentials' lines. Credentials not used since
// a restart count as accepted by the last poll that worked, which had to
// sign in; the website login, if any, reports its own session.
func (s *server) signIns(p PlatformInfo, latest []store.Run, web *webSyncRow) []signIn {
	var out []signIn
	if p.Credentials != nil {
		creds := p.Credentials()
		if len(creds) == 0 {
			out = append(out, signIn{Name: "No login", Label: "public", Parts: []signPart{{Text: "public programs only"}}})
		}
		for _, c := range creds {
			out = append(out, credentialLine(c, lastGoodPoll(p.Name, latest)))
		}
	}
	if web != nil {
		out = append(out, websiteLine(web.Session))
	}
	if s.VNC != nil && s.hasBrowser() {
		for i := range out {
			if out[i].Name == "Website login" {
				out[i].Watch = "/settings?tab=browser"
			}
		}
	}
	return out
}

func credentialLine(c platform.Credential, poll *time.Time) signIn {
	line := signIn{Name: c.Name}
	switch {
	case c.Failure != "":
		line.State, line.Label, line.Problem = "failed", "rejected", c.Failure
		line.Parts = []signPart{{Text: "refused", When: c.LastFail}}
		if c.Stuck {
			line.Parts = append(line.Parts, signPart{Text: "not retried until a restart"})
		}
	case c.LastOK != nil:
		line.State, line.Label, line.Parts = "ok", "ok", []signPart{{Text: "accepted", When: c.LastOK}}
		if c.SignedIn != nil {
			line.Parts = append(line.Parts, signPart{Text: "signed in", When: c.SignedIn})
		}
		if c.Expires != nil && c.Expires.Before(time.Now()) {
			break // a pasted session's last known expiry, gone by
		}
		if c.Expires != nil {
			line.Parts = append(line.Parts, signPart{Text: "expires", In: c.Expires})
		}
	case poll != nil:
		line.State, line.Label, line.Parts = "ok", "ok", []signPart{{Text: "accepted at the last poll", When: poll}}
	default:
		line.Label, line.Parts = "not checked", []signPart{{Text: "checked at the next poll"}}
	}
	return line
}

// lastGoodPoll is when the platform's latest poll that worked started.
func lastGoodPoll(name string, latest []store.Run) *time.Time {
	var at *time.Time
	for _, r := range latest {
		if r.Platform != name || r.Mode == "web" || !slices.Contains([]string{store.RunOK, store.RunPartial}, r.Status) {
			continue
		}
		if at == nil || r.StartedAt.After(*at) {
			at = &r.StartedAt
		}
	}
	return at
}

func websiteLine(ws platform.WebSession) signIn {
	line := signIn{Name: "Website login"}
	switch {
	case ws.Stuck:
		line.State, line.Label, line.Problem = "failed", "rejected", ws.Failure+" Fix it in .env and restart."
		line.Parts = []signPart{{Text: "refused", When: ws.LastFailure}}
	case ws.SignedIn:
		line.State, line.Label = "ok", "ok"
		line.Parts = []signPart{{Text: "signed in"}}
	case ws.Failure != "":
		line.State, line.Label, line.Problem = "failed", "failed", ws.Failure
		line.Parts = []signPart{{Text: "failed", When: ws.LastFailure}}
		if ws.RetryAfter != nil {
			line.Parts = append(line.Parts, signPart{Text: "tries again after", At: ws.RetryAfter})
		}
	default:
		line.Label, line.Parts = "not checked", []signPart{{Text: "signs in at the next run"}}
	}
	if ws.LastRenewal != nil && ws.SignedIn {
		line.Parts = append(line.Parts, signPart{Text: "renewed", When: ws.LastRenewal})
	}
	if ws.LastBrowserLogin != nil && ws.SignedIn {
		line.Parts = append(line.Parts, signPart{Text: "browser login", When: ws.LastBrowserLogin})
	}
	return line
}
