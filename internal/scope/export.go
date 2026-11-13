package scope

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/url"
	"regexp"
	"strings"
)

// Burp and ZAP scope exports: a program's in-scope targets as includes and
// its out-of-scope ones as excludes, in each tool's import format.
//
// Wildcards keep HuntDeck's meaning: "*.example.com" covers subdomains,
// not example.com itself. ZAP contexts can't express IP ranges, so CIDRs
// are listed in a comment instead.

// BurpRule is one rule of Burp Suite's advanced scope (Settings > Project >
// Scope, "Use advanced scope control"). Host, port and file are regexes.
type BurpRule struct {
	Enabled  bool   `json:"enabled"`
	File     string `json:"file,omitempty"`
	Host     string `json:"host"`
	Port     string `json:"port,omitempty"`
	Protocol string `json:"protocol"` // "any", "http" or "https"
}

// BurpScope returns a Burp project options file holding just the scope,
// for Settings > Project > Scope > Load (or Project options > Load).
func BurpScope(in, out []Target) ([]byte, error) {
	var doc struct {
		Target struct {
			Scope struct {
				AdvancedMode bool       `json:"advanced_mode"`
				Exclude      []BurpRule `json:"exclude"`
				Include      []BurpRule `json:"include"`
			} `json:"scope"`
		} `json:"target"`
	}
	doc.Target.Scope.AdvancedMode = true
	doc.Target.Scope.Include, doc.Target.Scope.Exclude = burpRules(in), burpRules(out)
	return json.MarshalIndent(doc, "", "  ")
}

func burpRules(targets []Target) []BurpRule {
	out := []BurpRule{}
	seen := map[BurpRule]bool{}
	for _, t := range targets {
		r, ok := burpRule(t)
		if ok && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func burpRule(t Target) (BurpRule, bool) {
	r := BurpRule{Enabled: true, Protocol: "any"}
	switch t.Kind {
	case KindWildcard:
		r.Host = "^" + hostPattern(strings.TrimPrefix(t.Value, "*."), strings.HasPrefix(t.Value, "*.")) + "$"
	case KindDomain, KindIP:
		r.Host = "^" + hostPattern(t.Value, false) + "$"
	case KindCIDR:
		r.Host = t.Value // Burp takes IP ranges as they are
	case KindURL:
		u, ok := parseTargetURL(t.Value)
		if !ok {
			return r, false
		}
		r.Host = "^" + hostPattern(u.Hostname(), false) + "$"
		if u.Scheme == "http" || u.Scheme == "https" {
			r.Protocol = u.Scheme
		}
		if p := u.Port(); p != "" {
			r.Port = "^" + p + "$"
		}
		if path := strings.TrimSuffix(u.EscapedPath(), "*"); path != "" && path != "/" {
			r.File = "^" + pathPattern(path) + ".*"
		}
	default:
		return r, false
	}
	return r, true
}

// ZAPContext returns a ZAP context file (File > Import Context) named name,
// in scope, with the targets as include and exclude regexes.
func ZAPContext(name string, in, out []Target) ([]byte, error) {
	type parser struct {
		Class  string `xml:"class"`
		Config string `xml:"config"`
	}
	type tech struct {
		Include []string `xml:"include"`
	}
	type context struct {
		Name       string   `xml:"name"`
		Desc       string   `xml:"desc"`
		InScope    bool     `xml:"inscope"`
		IncRegexes []string `xml:"incregexes"`
		ExcRegexes []string `xml:"excregexes"`
		// ZAP's top-level technologies; their children are included with them.
		Tech       tech   `xml:"tech"`
		URLParser  parser `xml:"urlparser"`
		PostParser parser `xml:"postparser"`
		Comment    string `xml:",comment"`
	}
	std := parser{Class: "org.zaproxy.zap.model.StandardParameterParser", Config: `{"kvps":"&","kvs":"=","struct":[]}`}
	c := context{Name: name, Desc: "Scope of " + name + ", exported by HuntDeck", InScope: true,
		Tech: tech{Include: []string{"Db", "Language", "OS", "SCM", "WS"}}, URLParser: std, PostParser: std}
	var skipped []string
	c.IncRegexes, skipped = zapRegexes(in)
	var skippedOut []string
	c.ExcRegexes, skippedOut = zapRegexes(out)
	if len(skipped) > 0 {
		c.Comment += " IP ranges in scope (ZAP regexes can't express them): " + strings.Join(skipped, " ") + " "
	}
	if len(skippedOut) > 0 {
		c.Comment += " IP ranges out of scope: " + strings.Join(skippedOut, " ") + " "
	}
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>` + "\n")
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(struct {
		XMLName xml.Name `xml:"configuration"`
		Context context  `xml:"context"`
	}{Context: c}); err != nil {
		return nil, err
	}
	buf.WriteString("\n")
	return buf.Bytes(), nil
}

// zapRegexes are whole-URL regexes for targets, and the CIDRs it can't
// express. They avoid commas: ZAP can't reload a context whose regexes
// have one.
func zapRegexes(targets []Target) (regexes, cidrs []string) {
	seen := map[string]bool{}
	add := func(r string) {
		if !seen[r] {
			seen[r] = true
			regexes = append(regexes, r)
		}
	}
	const rest = `(:[0-9]+)?(/.*)?$`
	for _, t := range targets {
		switch t.Kind {
		case KindWildcard:
			add(`^https?://` + hostPattern(strings.TrimPrefix(t.Value, "*."), strings.HasPrefix(t.Value, "*.")) + rest)
		case KindDomain, KindIP:
			add(`^https?://` + hostPattern(t.Value, false) + rest)
		case KindURL:
			u, ok := parseTargetURL(t.Value)
			if !ok {
				continue
			}
			scheme := `https?`
			if u.Scheme == "http" || u.Scheme == "https" {
				scheme = u.Scheme
			}
			port := `(:[0-9]+)?`
			if p := u.Port(); p != "" {
				port = ":" + p
			}
			path := strings.TrimSuffix(u.EscapedPath(), "*")
			if path == "" {
				path = "/"
			}
			add(`^` + scheme + `://` + hostPattern(u.Hostname(), false) + port + pathPattern(path) + `.*$`)
		case KindCIDR:
			cidrs = append(cidrs, t.Value)
		}
	}
	return regexes, cidrs
}

// hostPattern is a regex for a hostname that may hold '*' wildcards (as in
// "*-api-*.example.com"); sub, for a "*." wildcard, requires at least one
// subdomain label in front.
func hostPattern(host string, sub bool) string {
	parts := strings.Split(strings.ToLower(host), "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	pat := strings.Join(parts, `[^/]*`)
	if sub {
		pat = `([^./]+\.)+` + pat
	}
	return pat
}

// pathPattern is a regex for a path that may hold '*' wildcards, each
// spanning anything.
func pathPattern(p string) string {
	parts := strings.Split(p, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	return strings.Join(parts, ".*")
}

// parseTargetURL reads a URL target; one without a scheme is read as if it
// had one, and matched on any protocol.
func parseTargetURL(v string) (*url.URL, bool) {
	raw := v
	if !strings.Contains(raw, "://") {
		raw = "any://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}
	return u, true
}
