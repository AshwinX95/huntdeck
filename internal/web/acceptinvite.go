package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/store"
)

// acceptInvite accepts the invitation to a program from the Invites page,
// a Programs list or the board, then goes back where it came from with the
// outcome as a flash message.
func (s *server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	back := localPath(r.PostFormValue("back"), "/invites")
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	//nolint:gosec // localPath keeps back to a path on this site
	http.Redirect(w, r, back+sep+s.startAccept(r.PathValue("platform"), r.PathValue("handle")), http.StatusSeeOther)
}

// localPath is v if it's a path on this site (with its query), else def:
// never another site's address, "//elsewhere" included.
func localPath(v, def string) string {
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") ||
		strings.HasPrefix(u.Path, "//") || strings.ContainsAny(v, "\\\r\n") {
		return def
	}
	if u.RawQuery == "" {
		return u.EscapedPath()
	}
	return u.EscapedPath() + "?" + u.RawQuery
}

// startAccept accepts the invitation and returns the flash query for the
// outcome.
func (s *server) startAccept(platformName, handle string) string {
	if s.Poller == nil {
		return "err=poll-disabled"
	}
	err := s.Poller.AcceptInvite(canonicalPlatform(platformName), handle)
	switch {
	case err == nil:
		return "msg=invite-accepted"
	case errors.Is(err, ErrStillAccepting):
		return "msg=invite-accepting"
	case errors.Is(err, ErrNoInvite):
		return "err=invite-none"
	case errors.Is(err, ErrCannotAccept), errors.Is(err, ErrUnknownPlatform):
		return "err=invite-cannot"
	case errors.Is(err, ErrPlatformDisabled):
		return "err=invite-off"
	}
	// The platform's answer stays in the log: it can name the program.
	s.Log.Warn("accept invitation", "platform", platformName, "err", err)
	return "err=invite-failed"
}

func (s *server) apiAcceptInvite(w http.ResponseWriter, r *http.Request) {
	switch s.startAccept(r.PathValue("platform"), r.PathValue("handle")) {
	case "msg=invite-accepted":
		writeJSON(w, http.StatusOK, apiOKJSON{Status: "accepted"})
	case "msg=invite-accepting":
		writeJSON(w, http.StatusAccepted, apiOKJSON{Status: "accepting"})
	case "err=invite-none":
		writeJSONError(w, http.StatusNotFound, "no pending invitation to this program")
	case "err=invite-cannot":
		writeJSONError(w, http.StatusNotFound, "this platform's invitations can't be accepted from HuntDeck")
	case "err=invite-off":
		writeJSONError(w, http.StatusConflict, "platform disabled in Settings")
	case "err=invite-failed":
		writeJSONError(w, http.StatusBadGateway, "the platform didn't accept it; the details are in the server log")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "accepting invitations is unavailable")
	}
}

// discloses reports whether the platform publishes disclosed reports.
func (s *server) discloses(platformName string) bool {
	for _, p := range s.Platforms {
		if p.Name == platformName {
			return p.Discloses
		}
	}
	return false
}

// acceptsInvites reports whether invitations on the platform can be
// accepted from HuntDeck.
func (s *server) acceptsInvites(platformName string) bool {
	for _, p := range s.Platforms {
		if p.Name == platformName {
			return p.AcceptsInvites
		}
	}
	return false
}

// acceptForm is an Accept button's data: see the accept-invite template.
type acceptForm struct {
	Platform, Handle, Name string
	Back                   string // where to come back to: a path on this site
}

// acceptFor is the Accept button for p, or nil if it has no pending
// invitation that can be accepted from HuntDeck.
func (s *server) acceptFor(p store.ProgramListItem, back string) *acceptForm {
	if !p.InvitePending || !s.acceptsInvites(p.Platform) {
		return nil
	}
	return &acceptForm{Platform: p.Platform, Handle: p.Handle, Name: p.Name, Back: back}
}

// backTo is the page r asked for, without its flash message, to come back
// to after a form.
func backTo(r *http.Request) string {
	q := r.URL.Query()
	q.Del("msg")
	q.Del("err")
	if len(q) == 0 {
		return r.URL.Path
	}
	return r.URL.Path + "?" + q.Encode()
}
