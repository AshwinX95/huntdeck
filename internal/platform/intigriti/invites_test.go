package intigriti

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestListInvites(t *testing.T) {
	f := &fakeWeb{}
	logins := 0
	c := webClient(t, f, func(c *Client) error { logins++; return f.giveSSO(c) })
	got, err := c.ListInvites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []platform.Invite{{Handle: globexID, InvitedAt: time.Unix(1791288005, 0).UTC()}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("invites = %+v, want %+v (an entry without an id is skipped)", got, want)
	}
	if logins != 1 || f.hits["GET "+webAPI+"/invites"] != 1 {
		t.Errorf("logins %d, requests %v", logins, f.hits)
	}

	// KeepAlive checks the session with the same request, renewing it
	// when the app has dropped it.
	f.mu.Lock()
	f.expire = true
	f.mu.Unlock()
	if err := c.KeepAlive(context.Background()); err != nil || f.hits["GET "+webAPI+"/invites"] != 3 || f.sessions != 2 {
		t.Errorf("KeepAlive: %v, requests %v, app sessions %d", err, f.hits, f.sessions)
	}

	// Without the web login there's nothing to read or keep alive.
	api, err := New(Options{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.ListInvites(context.Background()); !errors.Is(err, ErrNoSession) {
		t.Errorf("without a web login: %v", err)
	}
	if err := api.KeepAlive(context.Background()); err != nil {
		t.Errorf("KeepAlive without a web login: %v", err)
	}
}

func TestAcceptInvite(t *testing.T) {
	f := &fakeWeb{}
	c := webClient(t, f, f.giveSSO)
	// The app has dropped the session: accepting renews it once.
	f.mu.Lock()
	f.expire = true
	f.mu.Unlock()
	if err := c.AcceptInvite(context.Background(), globexID); err != nil {
		t.Fatal(err)
	}
	if n := f.hits["POST "+webAPI+"/bug-bounty-programs/"+globexID+"/accept-invite"]; n != 2 {
		t.Errorf("accept requests = %d, want 2 (dropped session, then accepted)", n)
	}
	// An invitation that isn't there.
	if err := c.AcceptInvite(context.Background(), "00000000-0000-0000-0000-000000000000"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("unknown invitation: %v", err)
	}

	api, err := New(Options{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.AcceptInvite(context.Background(), globexID); !errors.Is(err, ErrNoSession) {
		t.Errorf("without a web login: %v", err)
	}
}
