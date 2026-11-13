package notify

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

var now = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func change(kind, field, oldV, newV string, entry *store.NotifyEntry) store.NotifyChange {
	return store.NotifyChange{ID: 7, OccurredAt: now, Kind: kind, Field: field, OldValue: oldV, NewValue: newV, Entry: entry,
		Program: store.NotifyProgram{ProgramRef: store.ProgramRef{ID: 1, Platform: "hackerone", Handle: "acme", Name: "Acme",
			URL: "https://hackerone.com/acme"}, OffersBounty: true, TopBounty: ptr(5000.0), Currency: "USD"}}
}

func TestFromChanges(t *testing.T) {
	wild := &store.NotifyEntry{Identifier: "*.acme.example", AssetType: scope.AssetWildcard, InScope: true, Bounty: true, NewRoot: true}
	url := &store.NotifyEntry{Identifier: "https://app.acme.example", AssetType: scope.AssetURL, InScope: true, Bounty: true}
	oos := &store.NotifyEntry{Identifier: "admin.acme.example", AssetType: scope.AssetDomain}
	invite := change(store.ChangeProgramAdded, "visibility", "", "private", nil)
	invite.Program.ID, invite.Program.Handle = 2, "secret"
	events := FromChanges([]store.NotifyChange{
		invite,
		change(store.ChangeScopeAdded, "in_scope", "", wild.Identifier, wild),
		change(store.ChangeScopeAdded, "out_of_scope", "", oos.Identifier, oos),
		change(store.ChangeScopeAdded, "in_scope", "", url.Identifier, url),
		change(store.ChangeStateChanged, "state", "paused", "open", nil),
		change(store.ChangeRewardsChanged, "rewards", `[{"critical":{"max":5000}}]`, `[{"critical":{"max":10000}}]`, nil),
		change(store.ChangePolicyChanged, store.FieldPolicy, "a", "b", nil),
		change(store.ChangePolicyChanged, store.FieldExclusions, "a", "b", nil),
		change(store.ChangeProgramRenamed, "name", "Acme", "Acme Inc", nil),
	})
	var got []string
	for _, e := range events {
		got = append(got, e.Kind)
	}
	if want := "invite scope_added scope_out reopened bounty_raised policy_changed"; strings.Join(got, " ") != want {
		t.Fatalf("kinds = %v, want %s", got, want)
	}
	if e := events[1]; e.count() != 2 || !e.Assets[0].NewRoot || e.Assets[1].Identifier != url.Identifier {
		t.Errorf("scope added = %+v", e)
	}
	if e := events[4]; e.Detail != "Top bounty $5,000 → $10,000" || e.ChangeID != 7 {
		t.Errorf("bounty raised = %+v", e)
	}
	if e := events[5]; e.Detail != "Edited: policy, exclusions" {
		t.Errorf("policy = %+v", e)
	}
	lower := FromChanges([]store.NotifyChange{change(store.ChangeRewardsChanged, "rewards", `[{"high":{"max":900}}]`, `[{"high":{"max":500}}]`, nil)})
	if len(lower) != 1 || lower[0].Kind != EventRewardsChanged {
		t.Errorf("lower bounties = %+v", lower)
	}
}

func TestRules(t *testing.T) {
	ev := func(kind string, mod func(*Program)) Event {
		p := &Program{Platform: "hackerone", Name: "Acme", Bounty: true, TopBounty: ptr(5000.0), Tags: []string{"focus"}}
		if mod != nil {
			mod(p)
		}
		return Event{Kind: kind, Platform: "hackerone", Program: p,
			Assets: []Asset{{Identifier: "a.acme.example", Type: scope.AssetDomain}, {Identifier: "*.acme.example", Type: scope.AssetWildcard}}}
	}
	def := DefaultRules()
	for _, tt := range []struct {
		name  string
		rules Rules
		e     Event
		want  bool
	}{
		{"default event", def, ev(EventScopeAdded, nil), true},
		{"event off by default", def, ev(EventPolicyChanged, nil), false},
		{"ignored program", def, ev(EventScopeAdded, func(p *Program) { p.ignored = true }), false},
		{"recovery follows problems", def, Event{Kind: EventPlatformRecovered, Platform: "bugcrowd"}, true},
		{"other platform", Rules{Events: def.Events, Platforms: []string{"bugcrowd"}}, ev(EventScopeAdded, nil), false},
		{"bounty only", Rules{Events: def.Events, BountyOnly: true}, ev(EventScopeAdded, func(p *Program) { p.Bounty = false }), false},
		{"private only", Rules{Events: def.Events, PrivateOnly: true}, ev(EventScopeAdded, nil), false},
		{"below min bounty", Rules{Events: def.Events, MinBounty: 10000}, ev(EventScopeAdded, nil), false},
		{"unknown bounty passes", Rules{Events: def.Events, MinBounty: 10000}, ev(EventScopeAdded, func(p *Program) { p.TopBounty = nil }), true},
		{"tag", Rules{Events: def.Events, Tags: []string{"focus", "x"}}, ev(EventScopeAdded, nil), true},
		{"missing tag", Rules{Events: def.Events, Tags: []string{"x"}}, ev(EventScopeAdded, nil), false},
		{"excluded tag", Rules{Events: def.Events, ExcludeTags: []string{"focus"}}, ev(EventScopeAdded, nil), false},
		{"major scope only, nothing major", Rules{Events: def.Events, MajorScopeOnly: true},
			Event{Kind: EventScopeAdded, Program: &Program{}, Assets: []Asset{{Type: scope.AssetURL}}}, false},
	} {
		if _, got := tt.rules.Match(tt.e); got != tt.want {
			t.Errorf("%s: matched %v", tt.name, got)
		}
	}
	m, ok := Rules{Events: def.Events, MajorScopeOnly: true}.Match(ev(EventScopeAdded, nil))
	if !ok || len(m.Assets) != 1 || m.Assets[0].Type != scope.AssetWildcard || m.count() != 1 {
		t.Errorf("major scope only kept %+v", m.Assets)
	}
}

func TestRenderEscapesAndSplits(t *testing.T) {
	var assets []Asset
	for range 25 {
		assets = append(assets, Asset{Identifier: "x`y.acme.example", Type: scope.AssetDomain, Bounty: true})
	}
	e := Event{Kind: EventScopeAdded, Platform: "bugcrowd", Assets: assets, AssetsTotal: 300,
		Program: &Program{Platform: "bugcrowd", Handle: "evil", Name: "@everyone *Evil* <b>&", Bounty: true, Private: true, URL: "https://bugcrowd.com/evil"}}
	d := renderer{st: discordStyle{}, base: "https://huntdeck.example", loc: time.UTC}.parts(store.OutboxPoll, []Event{e}, now, discordLimit)
	if len(d) != 1 {
		t.Fatalf("discord parts = %d", len(d))
	}
	for _, want := range []string{"🎯 **300 new in-scope assets** · Bugcrowd", "[**@\u200beveryone \\*Evil\\* <b\\>&**](https://huntdeck.example/programs/bugcrowd/evil)",
		"BBP · private", "• `xˋy.acme.example`", "[…and 290 more](https://huntdeck.example/programs/bugcrowd/evil)"} {
		if !strings.Contains(d[0], want) {
			t.Errorf("discord message lacks %q:\n%s", want, d[0])
		}
	}
	tg := renderer{st: telegramStyle{}, loc: time.UTC}.parts(store.OutboxPoll, []Event{e}, now, telegramLimit)
	for _, want := range []string{`<a href="https://bugcrowd.com/evil"><b>@everyone *Evil* &lt;b&gt;&amp;</b></a>`, "<code>x`y.acme.example</code>", "…and 290 more"} {
		if !strings.Contains(tg[0], want) {
			t.Errorf("telegram message lacks %q:\n%s", want, tg[0])
		}
	}

	// A digest of many events splits between events and caps the list.
	var many []Event
	for range 60 {
		many = append(many, e)
	}
	parts := renderer{st: discordStyle{}, loc: time.UTC}.parts(store.OutboxDigest, many, now, discordLimit)
	all := strings.Join(parts, "\n\n")
	if len(parts) < 5 || !strings.HasPrefix(parts[0], "**HuntDeck daily digest** · Tue 27 Oct · 60 updates") || !strings.Contains(all, "…and 20 more updates") {
		t.Errorf("digest: %d parts, starts %q", len(parts), parts[0][:60])
	}
	for _, p := range parts {
		if len(p) > discordLimit || !utf8.ValidString(p) {
			t.Errorf("part of %d bytes (valid UTF-8: %v)", len(p), utf8.ValidString(p))
		}
	}
	if got := chunk([]string{strings.Repeat("é", 3000)}, 100); len(got) != 1 || len(got[0]) > 100 || !utf8.ValidString(got[0]) {
		t.Errorf("an over-long line is cut badly: %d bytes", len(got[0]))
	}
}

func TestLastSlot(t *testing.T) {
	bne, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 26, 22, 30, 0, 0, time.UTC) // 08:30 on the 27th in Brisbane
	if got, want := LastSlot("09:00", at, bne), time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("before 9 = %v, want yesterday's %v", got, want)
	}
	if got, want := LastSlot("08:00", at, bne), time.Date(2026, 10, 26, 22, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("after 8 = %v, want %v", got, want)
	}
}

func TestConfigValidate(t *testing.T) {
	for _, tt := range []struct {
		kind string
		c    Config
		ok   bool
	}{
		{store.NotifyDiscord, Config{URL: "https://discord.com/api/webhooks/123/abc-DEF_1"}, true},
		{store.NotifyDiscord, Config{URL: "https://evil.example/api/webhooks/123/abc"}, false},
		{store.NotifyDiscord, Config{URL: "https://discord.com/api/webhooks/123/abc?x=1"}, false},
		{store.NotifyTelegram, Config{Token: "123456:AAbbCCddEEffGGhhIIjjKKllMM", ChatID: "-100123"}, true},
		{store.NotifyTelegram, Config{Token: "123456:AAbbCCddEEffGGhhIIjjKKllMM", ChatID: "me"}, false},
		{store.NotifyTelegram, Config{Token: "nope", ChatID: "1"}, false},
		{store.NotifyWebhook, Config{URL: "http://127.0.0.1:9000/hook"}, true},
		{store.NotifyWebhook, Config{URL: "ftp://x.example/"}, false},
		{store.NotifyWebhook, Config{URL: "https://user:pw@x.example/"}, false},
	} {
		if err := tt.c.Validate(tt.kind); (err == nil) != tt.ok {
			t.Errorf("%s %+v: %v", tt.kind, tt.c, err)
		}
	}
	if m := (Config{URL: "https://discord.com/api/webhooks/123/secret"}).Masked(store.NotifyDiscord); strings.Contains(m, "secret") {
		t.Errorf("masked = %q", m)
	}
}

// A flip-flopping program's state changes (store.FlipFlops or more in a
// week) aren't notified: not its pauses until one has lasted LongPause,
// nor its reopenings after a short pause. Others are, as ever.
func TestFlipFlopsHeldBack(t *testing.T) {
	ago := func(d time.Duration) *time.Time { at := now.Add(-d); return &at }
	tests := []struct {
		name string
		c    store.NotifyChange
		want string // the event's kind, "" for none
	}{
		{"a pause", store.NotifyChange{NewValue: "paused", Flips: 1}, EventPaused},
		{"a reopening", store.NotifyChange{NewValue: "open", Flips: 2, PrevStateAt: ago(3 * time.Hour)}, EventReopened},
		{"a flip-flopper's pause", store.NotifyChange{NewValue: "paused", Flips: 3}, ""},
		{"its reopening after hours", store.NotifyChange{NewValue: "open", Flips: 4, PrevStateAt: ago(3 * time.Hour)}, ""},
		{"its reopening after days", store.NotifyChange{NewValue: "open", Flips: 4, PrevStateAt: ago(50 * time.Hour)}, EventReopened},
		{"its pause, lasted", store.NotifyChange{NewValue: "paused", Flips: 5, LongPause: true}, EventPaused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.c
			c.ID, c.OccurredAt, c.Kind, c.Field = 9, now, store.ChangeStateChanged, "state"
			c.OldValue = map[string]string{"open": "paused", "paused": "open"}[c.NewValue]
			c.Program = store.NotifyProgram{ProgramRef: store.ProgramRef{ID: 1, Platform: "intigriti", Handle: "acme", Name: "Acme"}}
			got := ""
			for _, e := range FromChanges([]store.NotifyChange{c}) {
				got = e.Kind
				if c.LongPause && !strings.Contains(e.Detail, "Paused for over a day, after 4 status changes") {
					t.Errorf("detail %q", e.Detail)
				}
			}
			if got != tt.want {
				t.Errorf("event %q, want %q", got, tt.want)
			}
		})
	}
}
