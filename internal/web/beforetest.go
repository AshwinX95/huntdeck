package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/store"
)

// testRule is one thing to know before testing a program, for the program
// page's "Before you test" box: who may submit, what test traffic must
// carry, where accounts come from. Most come from what the platforms
// report; some only appear in the policy text and are read from it
// (FromPolicy), so the box says so.
type testRule struct {
	Kind       string // its icon: "req", "auto", "rate", "email", "header", "agent", "creds", "warn"
	Title      string
	Detail     string
	Copy       string // a value to copy (a header, a User-Agent); "" if none
	Alert      bool   // shown in red: it limits how you may test
	FromPolicy bool
}

// beforeYouTest lists p's rules, the platform's own first.
func beforeYouTest(p *store.ProgramDetail) []testRule {
	var out []testRule
	for _, r := range requires(p.Platform, p.Requirements) {
		title, detail, _ := strings.Cut(r.Title, "; ")
		out = append(out, testRule{Kind: "req", Title: upperFirst(title), Detail: upperFirst(detail)})
	}
	var std *store.ProgramStats
	if p.PublicStats != nil {
		std = p.PublicStats
		if ex := std.Profile.StandardsExclusions; len(ex) > 0 {
			names := make([]string, len(ex))
			for i, e := range ex {
				names[i] = e.Name
			}
			out = append(out, testRule{Kind: "warn", Title: fmt.Sprintf("Opts out of %d platform %s", len(ex), plural(len(ex), "standard", "standards")),
				Detail: strings.Join(names, "; ")})
		}
	}
	have := map[string]bool{}
	if t := p.Testing; t != nil {
		if t.NoAutomation {
			out = append(out, testRule{Kind: "auto", Title: "No automated tools", Detail: "Scanners and other automated tools aren't allowed.", Alert: true})
		}
		if t.RateLimit > 0 {
			out = append(out, testRule{Kind: "rate", Title: fmt.Sprintf("At most %s %s a second", strconv.FormatFloat(t.RateLimit, 'f', -1, 64), plural(int(t.RateLimit), "request", "requests")),
				Detail: "For automated tools."})
			have["rate"] = true
		}
		if t.PlatformEmail {
			out = append(out, testRule{Kind: "email", Title: "Sign up with your " + platformEmail(p.Platform) + " address", Detail: "Test accounts must use the platform's email alias."})
			have["email"] = true
		}
		if t.UserAgent != "" {
			out = append(out, testRule{Kind: "agent", Title: "User-Agent: " + t.UserAgent, Detail: "Send it with every request.", Copy: t.UserAgent})
			have["agent"] = true
		}
		if t.Header != "" {
			out = append(out, testRule{Kind: "header", Title: t.Header, Detail: "Send this header with every request.", Copy: t.Header})
			have["header"] = true
		}
	}
	for _, r := range policyRules(policyText(p)) {
		if !have[r.Kind] {
			out = append(out, r)
		}
	}
	switch {
	case len(p.CredentialAssets) > 0:
		out = append(out, testRule{Kind: "creds", Title: fmt.Sprintf("Test credentials for %d %s", len(p.CredentialAssets), plural(len(p.CredentialAssets), "asset", "assets")),
			Detail: strings.Join(p.CredentialAssets, ", ")})
	case std != nil && std.Profile.CredentialPool != nil:
		cp := std.Profile.CredentialPool
		d := "On request on " + platformLabel(p.Platform)
		if cp.Max > 0 {
			d += fmt.Sprintf(", up to %d %s each", cp.Max, plural(cp.Max, "account", "accounts"))
		}
		if !cp.Available {
			d += "; none left right now"
		}
		out = append(out, testRule{Kind: "creds", Title: "Test credentials", Detail: d + "."})
	}
	return out
}

// policyText is all of a program's own text: the policy and, where the
// platform splits it up, the rest.
func policyText(p *store.ProgramDetail) string {
	parts := []string{p.Policy}
	if p.PublicStats != nil {
		pr := p.PublicStats.Profile
		parts = append(parts, pr.Introduction, pr.InScope, pr.OutScope)
	}
	return strings.Join(parts, "\n")
}

// platformEmail is the email alias a platform gives researchers.
func platformEmail(platformName string) string {
	switch platformName {
	case "hackerone":
		return "@wearehackerone.com"
	case "bugcrowd":
		return "@bugcrowdninja.com"
	case "intigriti":
		return "@intigriti.me"
	case "yeswehack":
		return "@yeswehack.ninja"
	}
	return platformLabel(platformName)
}

var (
	// A request header a program asks for: "X-Bug-Bounty: <username>-twilio".
	policyHeaderRE = regexp.MustCompile(`\b(X-[A-Za-z0-9-]{2,40})\s*:\s*([^\s|*` + "`" + `,;]{1,80})`)
	// A User-Agent string: "User-Agent: HackerOne-<username>".
	policyAgentRE = regexp.MustCompile(`(?i)\bUser-Agent\s*(?:header\s*)?[:=]\s*` + "`?" + `([^\s|*` + "`" + `"][^|*` + "`" + `"\n]{2,100})`)
	// The platforms' email aliases.
	policyEmailRE = regexp.MustCompile(`(?i)@(wearehackerone\.com|bugcrowdninja\.com|intigriti\.me|yeswehack\.ninja)\b`)
	// A request rate: "5 requests per second", "10 req/s".
	policyRateRE = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*(?:requests?|reqs?)\s*(?:per|/|a|an)\s*(second|sec|s|minute|min)\b`)
)

// policyRules reads the rules programs only state in their text: a header
// to send, a User-Agent, the platform email to sign up with, a request
// rate. Each is the first one found.
func policyRules(text string) []testRule {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []testRule
	if m := policyHeaderRE.FindStringSubmatch(text); m != nil && !strings.EqualFold(m[1], "X-Frame-Options") && !strings.EqualFold(m[1], "X-Forwarded-For") {
		h := m[1] + ": " + strings.TrimRight(m[2], ".)")
		out = append(out, testRule{Kind: "header", Title: h, Detail: "Send this header with every request.", Copy: h, FromPolicy: true})
	}
	if m := policyAgentRE.FindStringSubmatch(text); m != nil {
		ua := strings.TrimRight(strings.TrimSpace(m[1]), ".)")
		out = append(out, testRule{Kind: "agent", Title: "User-Agent: " + ua, Detail: "Send it with every request.", Copy: ua, FromPolicy: true})
	}
	if m := policyEmailRE.FindStringSubmatch(text); m != nil {
		out = append(out, testRule{Kind: "email", Title: "Sign up with your @" + strings.ToLower(m[1]) + " address", Detail: "For test accounts.", FromPolicy: true})
	}
	if m := policyRateRE.FindStringSubmatch(text); m != nil {
		unit := "second"
		if strings.HasPrefix(strings.ToLower(m[2]), "m") {
			unit = "minute"
		}
		out = append(out, testRule{Kind: "rate", Title: fmt.Sprintf("At most %s requests a %s", m[1], unit), Detail: "For automated tools.", FromPolicy: true})
	}
	return out
}

// upperFirst capitalises s's first letter.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
