package browser

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for Chrome: run with
// FAKE_CHROME=1, it answers CDP commands on fds 3 and 4 like Chrome does.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CHROME") == "1" {
		fakeChrome()
		return
	}
	os.Exit(m.Run())
}

func fakeChrome() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("Chromium 141.0.7390.54")
		return
	}
	in, out := os.NewFile(3, "in"), os.NewFile(4, "out")
	r := bufio.NewReader(in)
	typed := ""
	answers := []string{} // replies to the Fetch events below
	clicks := []float64{} // where the mouse was pressed
	for {
		raw, err := r.ReadBytes(0)
		if err != nil {
			return
		}
		var m struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &m)
		var result any = map[string]any{}
		switch m.Method {
		case "Browser.close":
			return
		case "Fetch.enable":
			// A proxy challenge, a website's challenge, and a paused request.
			for _, e := range []string{
				`{"method":"Fetch.authRequired","params":{"requestId":"r1","authChallenge":{"source":"Proxy"}}}`,
				`{"method":"Fetch.authRequired","params":{"requestId":"r2","authChallenge":{"source":"Server"}}}`,
				`{"method":"Fetch.requestPaused","params":{"requestId":"r3"}}`,
			} {
				_, _ = out.Write(append([]byte(e), 0))
			}
		case "Fetch.continueWithAuth", "Fetch.continueRequest":
			b, _ := json.Marshal(m.Params)
			answers = append(answers, m.Method+" "+string(b))
		case "Target.createTarget":
			result = map[string]any{"targetId": "t1"}
		case "Target.attachToTarget":
			result = map[string]any{"sessionId": "s1"}
		case "Input.dispatchMouseEvent":
			if m.Params["type"] == "mousePressed" {
				x, _ := m.Params["x"].(float64)
				y, _ := m.Params["y"].(float64)
				clicks = append(clicks, x, y)
			}
		case "Input.dispatchKeyEvent":
			if t, ok := m.Params["text"].(string); ok {
				typed += t
			}
		case "Runtime.evaluate":
			expr, _ := m.Params["expression"].(string)
			switch {
			case strings.Contains(expr, "boom"):
				_, _ = out.Write(append([]byte(fmt.Sprintf(`{"id":%d,"result":{"result":{},"exceptionDetails":{"text":"Uncaught boom"}}}`, m.ID)), 0))
				continue
			case strings.Contains(expr, "String("): // WaitFor
				result = map[string]any{"result": map[string]any{"value": "true"}}
			case strings.Contains(expr, "typed"):
				result = map[string]any{"result": map[string]any{"value": typed}}
			case strings.Contains(expr, "getBoundingClientRect"):
				result = map[string]any{"result": map[string]any{"value": []float64{100, 200, 300, 40}}}
			case strings.Contains(expr, "clicks"):
				result = map[string]any{"result": map[string]any{"value": clicks}}
			case strings.Contains(expr, "answers"):
				result = map[string]any{"result": map[string]any{"value": answers}}
			default:
				result = map[string]any{"result": map[string]any{"value": true}}
			}
		case "Storage.getCookies":
			result = map[string]any{"cookies": []map[string]any{{"name": "sid", "value": "v", "domain": "app.example.com", "path": "/", "expires": -1, "httpOnly": true, "secure": true}}}
		case "Page.navigate":
			if u, _ := m.Params["url"].(string); strings.Contains(u, "bad") {
				result = map[string]any{"errorText": "net::ERR_NAME_NOT_RESOLVED"}
			}
		}
		// An event first, which the client must skip.
		_, _ = out.Write(append([]byte(`{"method":"Page.loadEventFired","params":{}}`), 0))
		b, _ := json.Marshal(map[string]any{"id": m.ID, "result": result})
		_, _ = out.Write(append(b, 0))
	}
}

func TestBrowser(t *testing.T) {
	t.Setenv("FAKE_CHROME", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := Start(ctx, Options{Path: exe, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	profile := b.profile
	p, err := b.NewPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Navigate(ctx, "https://login.example.com/"); err != nil {
		t.Fatal(err)
	}
	if err := p.Navigate(ctx, "https://bad.example/"); err == nil || !strings.Contains(err.Error(), "ERR_NAME_NOT_RESOLVED") {
		t.Errorf("navigate error = %v", err)
	}
	if err := p.Click(ctx, "#email"); err != nil {
		t.Fatal(err)
	}
	var at []float64
	if err := p.Eval(ctx, "clicks", &at); err != nil || len(at) != 2 || at[0] <= 100 || at[0] >= 400 || at[1] <= 200 || at[1] >= 240 {
		t.Errorf("clicked at %v (box 100,200 300x40), %v", at, err)
	}
	if err := p.Focus(ctx, "#email"); err != nil {
		t.Fatal(err)
	}
	if err := p.Type(ctx, "a@b.Ex#ample$", 0); err != nil {
		t.Fatal(err)
	}
	var typed string
	if err := p.Eval(ctx, "typed", &typed); err != nil || typed != "a@b.Ex#ample$" {
		t.Errorf("typed %q, %v", typed, err)
	}
	if err := p.Eval(ctx, "boom()", nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("script error = %v", err)
	}
	if s, err := p.WaitFor(ctx, "true", time.Second); err != nil || s != "true" {
		t.Errorf("WaitFor = %q, %v", s, err)
	}
	cs, err := b.Cookies(ctx)
	if err != nil || len(cs) != 1 || cs[0].Name != "sid" || cs[0].Domain != "app.example.com" || !cs[0].Expires.IsZero() {
		t.Errorf("cookies = %+v, %v", cs, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Errorf("profile %s still there", profile)
	}
	if _, err := b.Cookies(ctx); err == nil {
		t.Error("a closed browser still answers")
	}
}

func TestUserAgent(t *testing.T) {
	t.Setenv("FAKE_CHROME", "1")
	exe, _ := os.Executable()
	if ua := userAgent(context.Background(), exe); !strings.Contains(ua, "Chrome/141.0.7390.54 ") || strings.Contains(ua, "Headless") {
		t.Errorf("userAgent = %q", ua)
	}
}

// A windowed Chrome without a display gets a virtual one, which Close ends.
func TestHeadedStartsXvfb(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("no Xvfb")
	}
	t.Setenv("FAKE_CHROME", "1")
	t.Setenv("DISPLAY", "")
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := Start(ctx, Options{Path: exe})
	if err != nil {
		t.Fatal(err)
	}
	if b.xvfb == nil || b.xvfb.Process == nil {
		t.Fatal("no virtual display started")
	}
	if !strings.Contains(strings.Join(b.cmd.Env, " "), "DISPLAY=:") {
		t.Error("Chrome wasn't given the display")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.xvfb.ProcessState == nil {
		t.Error("Xvfb still running after Close")
	}
}

// A proxy's credentials stay off Chrome's command line: Chrome gets the
// address, and the proxy's challenge (only the proxy's) gets the credentials.
func TestProxyAuth(t *testing.T) {
	t.Setenv("FAKE_CHROME", "1")
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	proxy, _ := url.Parse("http://user1:pa%40ss@proxy.example.net:3128")
	b, err := Start(ctx, Options{Path: exe, Headless: true, Proxy: proxy})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	args := strings.Join(b.cmd.Args, " ")
	if !strings.Contains(args, "--proxy-server=http://proxy.example.net:3128") || strings.Contains(args, "user1") || strings.Contains(args, "pa@ss") {
		t.Errorf("args = %s", args)
	}
	p, err := b.NewPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ // sorted
		`Fetch.continueRequest {"requestId":"r3"}`,
		`Fetch.continueWithAuth {"authChallengeResponse":{"password":"pa@ss","response":"ProvideCredentials","username":"user1"},"requestId":"r1"}`,
		`Fetch.continueWithAuth {"authChallengeResponse":{"response":"CancelAuth"},"requestId":"r2"}`,
	}
	// The replies go out asynchronously; wait for all three.
	var got []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := p.Eval(ctx, "answers", &got); err != nil {
			t.Fatal(err)
		}
		if len(got) >= len(want) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("answers =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	for _, bad := range []string{"socks5://proxy.example.net:1080", "http://"} {
		u, _ := url.Parse(bad)
		if _, err := Start(ctx, Options{Path: exe, Headless: true, Proxy: u}); err == nil {
			t.Errorf("Start accepted proxy %q", bad)
		}
	}
}

// A kept profile survives Close, minus the stale lock a killed Chrome
// leaves; the time zone reaches Chrome's environment.
func TestKeptProfile(t *testing.T) {
	t.Setenv("FAKE_CHROME", "1")
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "chrome")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("otherhost-123", filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	b, err := Start(ctx, Options{Path: exe, Headless: true, Profile: dir, TimeZone: "America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "SingletonLock")); !os.IsNotExist(err) {
		t.Error("stale lock left in place")
	}
	if !slices.Contains(b.cmd.Env, "TZ=America/New_York") {
		t.Error("Chrome wasn't given the time zone")
	}
	if !slices.Contains(b.cmd.Args, "--user-data-dir="+dir) || !slices.Contains(b.cmd.Args, "--force-webrtc-ip-handling-policy=disable_non_proxied_udp") {
		t.Errorf("args = %v", b.cmd.Args)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("kept profile removed: %v", err)
	}
}

func TestKeyFor(t *testing.T) {
	for _, c := range []struct {
		r     rune
		want  key
		known bool
	}{
		{'a', key{"KeyA", 65, false}, true},
		{'Q', key{"KeyQ", 81, true}, true},
		{'7', key{"Digit7", 55, false}, true},
		{'#', key{"Digit3", 51, true}, true},
		{'$', key{"Digit4", 52, true}, true},
		{'.', key{"Period", 190, false}, true},
		{'"', key{"Quote", 222, true}, true},
		{'é', key{}, false},
	} {
		if got, ok := keyFor(c.r); got != c.want || ok != c.known {
			t.Errorf("keyFor(%q) = %+v, %v; want %+v, %v", c.r, got, ok, c.want, c.known)
		}
	}
}
