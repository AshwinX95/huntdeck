package bugcrowd

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestInvitedProgramsListed(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Session: goodSession})
	ctx := context.Background()
	l, err := c.ListPrograms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete {
		t.Error("listing with invitations read should be complete")
	}
	var got *platform.Program
	for i, p := range l.Programs {
		if p.Handle == "demo-xyz" {
			t.Error("demo invitation listed")
		}
		if p.Handle == "wayne-bb" {
			got = &l.Programs[i]
		}
	}
	if got == nil {
		t.Fatal("invited program wayne-bb not listed")
	}
	if got.Name != "Wayne Enterprises" || !got.Private || got.State != platform.StateOpen || !got.OffersBounty ||
		got.Currency != "USD" || bountyRange(*got) != "250..6000" || got.TriageService != "Platform" || got.Industry != "Technology" ||
		got.URL != f.base.URL+"/engagements/invites/1a000000-0000-4000-8000-000000000001" || got.StartedAt == nil || len(got.Raw) == 0 {
		t.Errorf("invited program: %+v (range %s)", *got, bountyRange(*got))
	}

	// ListInvites reuses what the listing read.
	inv, err := c.ListInvites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 1 || inv[0].Handle != "wayne-bb" || f.hit("/engagements/invites.json") != 1 {
		t.Errorf("invites %+v after %d requests, want wayne-bb from the listing's one", inv, f.hit("/engagements/invites.json"))
	}

	// Its scope and details come from the invitation: the brief is 404.
	entries, d, err := c.FetchScopeDetail(ctx, *got)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Identifier)
		if e.Identifier == "https://blog.wayne.example.com" && (e.InScope || e.BountyEligible) {
			t.Errorf("out-of-scope target: %+v", e)
		}
		if e.Identifier == "https://app.wayne.example.com" && (!e.InScope || !e.BountyEligible) {
			t.Errorf("in-scope target: %+v", e)
		}
	}
	if !slices.Equal(ids, []string{"https://app.wayne.example.com", "*.api.wayne.example.com", "https://blog.wayne.example.com"}) {
		t.Errorf("scope = %v", ids)
	}
	if len(d.Rewards) != 1 || d.Rewards[0].Critical.Max != 6000 || d.Policy == "" || d.State != platform.StateOpen ||
		d.Facts == nil || d.Facts.SafeHarbor != "full" {
		t.Errorf("detail = %+v", d)
	}
	if at, err := c.ScopeChangedAt(ctx, *got); err != nil || !at.IsZero() {
		t.Errorf("ScopeChangedAt = %v, %v", at, err)
	}
	if f.hit("/engagements/wayne-bb") != 0 || f.hit("/engagements/wayne-bb/changelog.json") != 0 {
		t.Error("the unaccepted brief was requested")
	}
}

func TestInvitesUnreadMakesListingIncomplete(t *testing.T) {
	f := newFake(t)
	f.invitesFail = true
	c := f.client(t, Options{Session: goodSession})
	l, err := c.ListPrograms(context.Background())
	if err == nil || l.Complete || len(l.Programs) == 0 {
		t.Errorf("complete=%v programs=%d err=%v; want the programs, incomplete, with an error", l.Complete, len(l.Programs), err)
	}
}

func TestInvitesPublicOnly(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.hit("/engagements/invites.json") != 0 {
		t.Error("public mode read invitations")
	}
	if _, err := c.ListInvites(context.Background()); !errors.Is(err, platform.ErrNoWebLogin) {
		t.Errorf("ListInvites err = %v, want ErrNoWebLogin", err)
	}
	if err := c.AcceptInvite(context.Background(), "wayne-bb"); !errors.Is(err, platform.ErrNoWebLogin) {
		t.Errorf("AcceptInvite err = %v, want ErrNoWebLogin", err)
	}
}

func TestAcceptInvite(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Session: goodSession})
	ctx := context.Background()
	if err := c.AcceptInvite(ctx, "acme-bb"); err == nil {
		t.Error("accepting a program without an invitation succeeded")
	}
	if err := c.AcceptInvite(ctx, "wayne-bb"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.accepted, []string{"1a000000-0000-4000-8000-000000000001"}) {
		t.Errorf("accepted = %v", f.accepted)
	}
	if _, ok := c.invited("wayne-bb"); ok {
		t.Error("accepted invitation still held: its scope would come from it, not the brief")
	}
}

func TestMetaContent(t *testing.T) {
	for _, tt := range []struct{ html, want string }{
		{`<meta name="csrf-token" content="abc">`, "abc"},
		{`<meta content="abc" name="csrf-token"/>`, "abc"},
		{`<meta name="csrf-param" content="x"><meta name="csrf-token" content="y">`, "y"},
		{`<meta name="other" content="x">`, ""},
		{``, ""},
	} {
		if got := metaContent([]byte(tt.html), "csrf-token"); got != tt.want {
			t.Errorf("%q: %q, want %q", tt.html, got, tt.want)
		}
	}
}
