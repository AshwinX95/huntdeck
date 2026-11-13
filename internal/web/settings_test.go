package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/scheduler"
)

func TestPollNowFromSettings(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, _ := e.do(c, "POST", "/settings/poll", url.Values{"platform": {"fake"}, "mode": {"full"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings?msg=poll-started" {
		t.Errorf("poll now: %d → %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, body := e.do(c, "GET", "/settings?msg=poll-started", nil, nil)
	if !strings.Contains(body, "Poll started") {
		t.Error("flash message missing")
	}
	_, body = e.do(c, "GET", "/settings?msg=<b>injected</b>", nil, nil)
	if strings.Contains(body, "injected") {
		t.Error("arbitrary flash text was reflected")
	}
}

func TestEditScheduleFromSettings(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	defaults := scheduler.Settings{Enabled: true, Quick: 15 * time.Minute, Full: 6 * time.Hour}
	for _, tt := range []struct {
		name     string
		form     url.Values
		wantCode int
		wantBody string
		want     scheduler.Settings
	}{
		{"save", url.Values{"platform": {"fake"}, "enabled": {"1"}, "quick": {"30m"}, "full": {"1d"}},
			http.StatusSeeOther, "", scheduler.Settings{Enabled: true, Quick: 30 * time.Minute, Full: 24 * time.Hour}},
		{"disable", url.Values{"platform": {"fake"}, "quick": {"30m"}, "full": {"off"}},
			http.StatusSeeOther, "", scheduler.Settings{Quick: 30 * time.Minute}},
		{"too often", url.Values{"platform": {"fake"}, "enabled": {"1"}, "quick": {"1m"}, "full": {"6h"}},
			http.StatusBadRequest, "quick poll interval must be off or between 5m", scheduler.Settings{Quick: 30 * time.Minute}},
		{"quick slower than full", url.Values{"platform": {"fake"}, "enabled": {"1"}, "quick": {"12h"}, "full": {"6h"}},
			http.StatusBadRequest, "more often than the full poll", scheduler.Settings{Quick: 30 * time.Minute}},
		{"garbage", url.Values{"platform": {"fake"}, "enabled": {"1"}, "quick": {"<b>soon</b>"}, "full": {"6h"}},
			http.StatusBadRequest, "isn&#39;t an interval", scheduler.Settings{Quick: 30 * time.Minute}},
		{"unknown platform", url.Values{"platform": {"nope"}, "enabled": {"1"}, "quick": {"30m"}, "full": {"6h"}},
			http.StatusNotFound, "isn&#39;t configured", scheduler.Settings{Quick: 30 * time.Minute}},
	} {
		res, body := e.do(c, "POST", "/settings/platform", tt.form, nil)
		if res.StatusCode != tt.wantCode {
			t.Errorf("%s: status %d, want %d", tt.name, res.StatusCode, tt.wantCode)
		}
		if tt.wantCode == http.StatusSeeOther && res.Header.Get("Location") != "/settings?msg=settings-saved" {
			t.Errorf("%s: redirected to %s", tt.name, res.Header.Get("Location"))
		}
		if !strings.Contains(body, tt.wantBody) || strings.Contains(body, "<b>soon") {
			t.Errorf("%s: body lacks %q or reflects unescaped input", tt.name, tt.wantBody)
		}
		if got := e.sched.Get("fake"); got != tt.want {
			t.Errorf("%s: settings = %+v, want %+v", tt.name, got, tt.want)
		}
	}

	_, body := e.do(c, "GET", "/settings", nil, nil)
	for _, want := range []string{`class="chip disabled"`, "default 15m", "/settings/platform/reset"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}

	res, _ := e.do(c, "POST", "/settings/platform/reset", url.Values{"platform": {"fake"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings?msg=settings-reset" {
		t.Errorf("reset: %d → %s", res.StatusCode, res.Header.Get("Location"))
	}
	if got := e.sched.Get("fake"); got != defaults || e.sched.Overridden("fake") {
		t.Errorf("after reset settings = %+v, want defaults %+v", got, defaults)
	}
}

// Settings saves every platform's intervals at once, only if they're all
// valid, and leaves unchanged ones following .env.
func TestSaveAllIntervals(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	bad := url.Values{"platform": {"fake"}, "enabled_fake": {"1"}, "quick_fake": {"1m"}, "full_fake": {"6h"}}
	if res, body := e.do(c, "POST", "/settings/platform", bad, nil); res.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "Fake: quick poll interval") && !strings.Contains(body, "quick poll interval must be") {
		t.Errorf("bad interval: %d", res.StatusCode)
	}
	same := url.Values{"platform": {"fake"}, "enabled_fake": {"1"}, "quick_fake": {"15m"}, "full_fake": {"6h"}}
	if res, _ := e.do(c, "POST", "/settings/platform", same, nil); res.StatusCode != http.StatusSeeOther || e.sched.Overridden("fake") {
		t.Errorf("unchanged: %d, overridden %v", res.StatusCode, e.sched.Overridden("fake"))
	}
	same.Set("full_fake", "12h")
	if res, _ := e.do(c, "POST", "/settings/platform", same, nil); res.StatusCode != http.StatusSeeOther || e.sched.Get("fake").Full != 12*time.Hour {
		t.Errorf("changed: %d, %+v", res.StatusCode, e.sched.Get("fake"))
	}
	// A form shown again with an error stays on its tab.
	if _, body := e.do(c, "POST", "/settings/huntscore", url.Values{"preset": {"lots"}}, nil); !strings.Contains(body, `<a href="/settings?tab=you" class="on"`) {
		t.Error("the HuntScore form's error isn't shown on its tab")
	}
}

func TestPollRefusedForDisabledPlatform(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.poller.err = ErrPlatformDisabled
	res, _ := e.do(c, "POST", "/settings/poll", url.Values{"platform": {"fake"}, "mode": {"quick"}}, nil)
	if loc := res.Header.Get("Location"); res.StatusCode != http.StatusSeeOther || !strings.Contains(loc, "err=poll-off") {
		t.Errorf("settings poll: %d → %s", res.StatusCode, loc)
	}
	res, _ = e.do(c, "POST", "/api/v1/poll/fake?mode=quick", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("API poll of a disabled platform = %d, want 409", res.StatusCode)
	}
}

func TestTimezoneSetting(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()

	_, body := e.do(c, http.MethodGet, "/settings?tab=display", nil, nil)
	// Every zone is listed with its country and offset, the one in use
	// marked; a search finds Brisbane by city, country or offset.
	for _, want := range []string{`name="timezone" value="UTC"`, `role="combobox"`, `<script src="/static/combo.js?v=`,
		`data-value="UTC" data-search="utc  utc&#43;00:00 " aria-selected="true"><span class="cv">UTC</span><span class="cm">UTC&#43;00:00</span>`,
		`data-value="Australia/Brisbane" data-search="australia brisbane australia utc&#43;10:00 aest"><span class="cv">Australia/Brisbane</span><span class="cm">Australia · UTC&#43;10:00 · AEST</span>`,
		`data-value="America/St_Johns"`, `<span class="cm">Canada · UTC−03:30 · NST</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	if n := strings.Count(body, `role="option"`); n < 400 {
		t.Errorf("%d zones listed, want every one", n)
	}
	_, body = e.do(c, http.MethodGet, "/changes?since=all", nil, nil)
	if !strings.Contains(body, " UTC\"") {
		t.Errorf("times aren't in UTC by default")
	}

	for _, bad := range []string{"Brisbane", "Local", "", "<script>"} {
		if res, body := e.do(c, http.MethodPost, "/settings/timezone", url.Values{"timezone": {bad}}, nil); res.StatusCode != http.StatusBadRequest ||
			!strings.Contains(body, "Unknown time zone") || strings.Contains(body, "<script>") {
			t.Errorf("zone %q: %d", bad, res.StatusCode)
		}
	}
	res, _ := e.do(c, http.MethodPost, "/settings/timezone", url.Values{"timezone": {"Australia/Brisbane"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings?tab=display&msg=timezone-saved" {
		t.Fatalf("save: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, body = e.do(c, http.MethodGet, "/changes?since=all", nil, nil)
	if !strings.Contains(body, " AEST\"") || strings.Contains(body, " UTC\"") {
		t.Errorf("times aren't in Brisbane time after saving it")
	}
	_, body = e.do(c, http.MethodGet, "/settings?tab=display", nil, nil)
	if !strings.Contains(body, `value="Australia/Brisbane"`) || !strings.Contains(body, "Reset to UTC") {
		t.Errorf("settings don't show the saved zone")
	}

	res, _ = e.do(c, http.MethodPost, "/settings/timezone", url.Values{"reset": {"1"}}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset: %d", res.StatusCode)
	}
	_, body = e.do(c, http.MethodGet, "/changes?since=all", nil, nil)
	if !strings.Contains(body, " UTC\"") {
		t.Errorf("reset didn't go back to the default")
	}
}

func TestNotificationChannels(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()

	_, body := e.do(c, http.MethodGet, "/settings/notifications", nil, nil)
	if !strings.Contains(body, "No channels yet") || !strings.Contains(body, `href="/settings/notifications/new?kind=telegram"`) {
		t.Error("empty notifications page is wrong")
	}
	_, body = e.do(c, http.MethodGet, "/settings/notifications/new?kind=discord", nil, nil)
	if !strings.Contains(body, `name="event_invite" value="1" checked`) || strings.Contains(body, `name="event_policy_changed" value="1" checked`) {
		t.Error("a new channel doesn't start with the default events")
	}

	form := url.Values{"kind": {"discord"}, "name": {"<b>Phone</b>"}, "url": {"https://evil.example/api/webhooks/1/x"},
		"event_invite": {"1"}, "event_scope_added": {"1"}, "instant": {"1"}, "enabled": {"1"}, "tags": {"focus"}}
	res, body := e.do(c, http.MethodPost, "/settings/notifications", form, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Discord webhook URL should look like") || strings.Contains(body, "<b>Phone") {
		t.Errorf("bad Discord URL: %d", res.StatusCode)
	}
	form.Set("url", "https://discord.com/api/webhooks/123/supersecret")
	if res, _ = e.do(c, http.MethodPost, "/settings/notifications", form, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("add channel: %d", res.StatusCode)
	}
	chans, err := e.store.NotifyChannels(ctx)
	if err != nil || len(chans) != 1 {
		t.Fatalf("channels = %+v, %v", chans, err)
	}
	id := strconv.FormatInt(chans[0].ID, 10)
	rules := notify.ParseRules(chans[0].Rules)
	if !slices.Equal(rules.Events, []string{"invite", "scope_added"}) || !slices.Equal(rules.Tags, []string{"focus"}) {
		t.Errorf("rules = %+v", rules)
	}
	for _, path := range []string{"/settings/notifications", "/settings/notifications/" + id} {
		_, body = e.do(c, http.MethodGet, path, nil, nil)
		if strings.Contains(body, "supersecret") || !strings.Contains(body, "&lt;b&gt;Phone&lt;/b&gt;") {
			t.Errorf("%s shows the secret or an unescaped name", path)
		}
	}

	// Saving without the URL keeps it; switching both deliveries off is refused.
	form.Del("url")
	form.Set("name", "Phone")
	form.Set("digest_at", "09:00")
	if res, _ = e.do(c, http.MethodPost, "/settings/notifications/"+id, form, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit channel: %d", res.StatusCode)
	}
	ch, err := e.store.NotifyChannel(ctx, chans[0].ID)
	if err != nil || notify.ParseConfig(ch.Config).URL != "https://discord.com/api/webhooks/123/supersecret" || ch.DigestAt != "09:00" ||
		ch.DigestSentAt == nil || ch.Name != "Phone" {
		t.Errorf("after edit = %+v, %v", ch, err)
	}
	form.Del("instant")
	form.Del("digest_at")
	if res, body = e.do(c, http.MethodPost, "/settings/notifications/"+id, form, nil); res.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "a daily digest, or both") {
		t.Errorf("no delivery: %d", res.StatusCode)
	}

	// Send test reaches a webhook now and reports failures.
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, string(b))
		if strings.Contains(r.URL.Path, "broken") {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer hook.Close()
	add := func(name, path string) string {
		t.Helper()
		res, _ := e.do(c, http.MethodPost, "/settings/notifications", url.Values{"kind": {"webhook"}, "name": {name},
			"url": {hook.URL + path}, "event_invite": {"1"}, "instant": {"1"}, "enabled": {"1"}}, nil)
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("add webhook: %d", res.StatusCode)
		}
		all, _ := e.store.NotifyChannels(ctx)
		for _, ch := range all {
			if ch.Name == name {
				return strconv.FormatInt(ch.ID, 10)
			}
		}
		t.Fatal("webhook not saved")
		return ""
	}
	ok, broken := add("hook", "/ok"), add("broken", "/broken")
	if res, _ = e.do(c, http.MethodPost, "/settings/notifications/"+ok+"/test", nil, nil); res.StatusCode != http.StatusSeeOther ||
		len(got) != 1 || !strings.Contains(got[0], `"type":"test"`) {
		t.Errorf("test send: %d, %q", res.StatusCode, got)
	}
	if res, body = e.do(c, http.MethodPost, "/settings/notifications/"+broken+"/test", nil, nil); res.StatusCode != http.StatusBadGateway ||
		!strings.Contains(body, "The test message to broken failed: HTTP 500") || strings.Contains(body, "/broken") {
		t.Errorf("failed test send: %d", res.StatusCode)
	}

	// The channel form tests the address as typed, saved or not: inline for
	// its script, or by showing the form again.
	draft := url.Values{"kind": {"webhook"}, "name": {"Draft"}, "url": {hook.URL + "/draft"}}
	res, body = e.do(c, http.MethodPost, "/settings/notifications/test?inline=1", draft, nil)
	if res.StatusCode != http.StatusOK || body != "Test message sent to Draft." || len(got) != 3 {
		t.Errorf("draft test: %d %q", res.StatusCode, body)
	}
	draft.Del("url")
	if res, body = e.do(c, http.MethodPost, "/settings/notifications/test?inline=1", draft, nil); res.StatusCode != http.StatusBadRequest ||
		strings.Contains(body, "<") {
		t.Errorf("draft test without an address: %d %q", res.StatusCode, body)
	}
	edited := url.Values{"name": {"hook renamed"}, "event_invite": {"1"}, "instant": {"1"}}
	if res, body = e.do(c, http.MethodPost, "/settings/notifications/"+ok+"/test", edited, nil); res.StatusCode != http.StatusOK ||
		!strings.Contains(body, "Test message sent to hook renamed.") || !strings.Contains(body, `class="channel-form"`) {
		t.Errorf("test from the form: %d", res.StatusCode)
	}
	if all, _ := e.store.NotifyChannels(ctx); len(all) != 3 {
		t.Errorf("testing saved a channel: %d channels", len(all))
	}
	_, body = e.do(c, http.MethodGet, "/settings/notifications", nil, nil)
	if !strings.Contains(body, "Invites, scope added · tagged focus") || !strings.Contains(body, `data-href="/settings/notifications/`+ok+`"`) {
		t.Error("channel rows don't say what they send")
	}

	// The form's preview follows its choices.
	for _, tt := range []struct{ query, want string }{
		{"event_launch=1&event_scope_added=1&instant=1&enabled=1", `<p class="pv-stat">`},
		{"event_launch=1", "this channel sends nothing"},
		{"event_launch=1&instant=1&min_bounty=lots", `<p class="pv-stat">`},
	} {
		res, body := e.do(c, http.MethodGet, "/settings/notifications/preview?"+tt.query, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, tt.want) || strings.Contains(body, "<html") {
			t.Errorf("preview %s: %d %q", tt.query, res.StatusCode, body)
		}
	}

	if res, _ = e.do(c, http.MethodPost, "/settings/notifications/address", url.Values{"base_url": {"javascript:alert(1)"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad address: %d", res.StatusCode)
	}
	if res, _ = e.do(c, http.MethodPost, "/settings/notifications/address", url.Values{"base_url": {"https://huntdeck.tailnet.example/"}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("save address: %d", res.StatusCode)
	}
	_, body = e.do(c, http.MethodGet, "/settings/notifications", nil, nil)
	if !strings.Contains(body, `value="https://huntdeck.tailnet.example"`) {
		t.Error("address not saved")
	}
	if res, _ = e.do(c, http.MethodPost, "/settings/notifications/"+id+"/delete", nil, nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("delete: %d", res.StatusCode)
	}
	if res, _ = e.do(c, http.MethodGet, "/settings/notifications/"+id, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("deleted channel: %d", res.StatusCode)
	}
}

// Every zone in the picker loads, so any choice saves.
func TestZoneTableLoads(t *testing.T) {
	for _, z := range zoneTable {
		if _, err := time.LoadLocation(z.Name); err != nil {
			t.Errorf("%s: %v", z.Name, err)
		}
	}
	// A zone in use that isn't in the table still shows, first.
	if c := zoneChoices(time.Now(), "US/Eastern"); c[0].Name != "US/Eastern" || !c[0].On {
		t.Errorf("an old zone name in use: %+v", c[0])
	}
}

// A read-only copy is seen from anywhere: it gives times in UTC (app.js
// puts them in the visitor's zone), whatever zone its owner set.
func TestReadOnlyTimesInUTC(t *testing.T) {
	for _, ro := range []bool{false, true} {
		e := newTestEnv(t, func(d *Deps) { d.ReadOnly, d.Timezone = ro, "America/New_York" })
		_, body := e.do(e.client(), http.MethodGet, "/programs", nil, nil)
		utc, eastern := strings.Contains(body, " UTC\">Updated"), strings.Contains(body, " EST\">Updated") || strings.Contains(body, " EDT\">Updated")
		if utc != ro || eastern == ro {
			t.Errorf("read-only %v: UTC %v, Eastern %v", ro, utc, eastern)
		}
		// The note says both when the data is from and how often it's refreshed.
		if ro && !strings.Contains(body, "live public platform data, last refreshed from the platforms 20") {
			t.Error("the demo note doesn't say when its data is from")
		}
		// Digest times stay the server's; the page says which times are whose.
		_, notes := e.do(e.client(), http.MethodGet, "/settings/notifications", nil, nil)
		if ro != strings.Contains(notes, "Digest times are set in UTC, as the server sends them; other dates and times here show in your own time zone.") {
			t.Errorf("read-only %v: notifications page's time note", ro)
		}
	}
}
