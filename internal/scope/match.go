package scope

import (
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Rule is one target belonging to a program's in- or out-of-scope list.
type Rule struct {
	ProgramID int64
	InScope   bool
	Target    Target
}

// ProgramMatch is the verdict for one program.
type ProgramMatch struct {
	ProgramID int64
	InScope   bool
	Rule      Rule // the most specific rule that decided the verdict
}

// Match reports, for every program with a matching rule, whether input (a
// hostname, IP or URL) is in or out of scope.
//
// The most specific rule wins: a path rule beats its host, an exact host
// beats any wildcard, and a longer wildcard base beats a shorter one. On a
// tie, out-of-scope wins. A wildcard "*.example.com" matches subdomains
// only, not "example.com" itself. A rule with a path (a URL target, as
// "https://github.com/example-org/*") covers only URLs under that path:
// never the bare host, nor its other paths.
func Match(rules []Rule, input string) []ProgramMatch {
	host, ok := hostOf(input)
	if !ok {
		return nil
	}
	inPath := pathOf(input)
	addr, err := netip.ParseAddr(host)
	isIP := err == nil

	type best struct {
		rule Rule
		spec int
	}
	byProgram := map[int64]best{}
	var order []int64
	for _, r := range rules {
		spec := specificity(r.Target, host, inPath, addr, isIP)
		if spec == 0 {
			continue
		}
		cur, seen := byProgram[r.ProgramID]
		if !seen {
			order = append(order, r.ProgramID)
		}
		if !seen || spec > cur.spec || (spec == cur.spec && !r.InScope) {
			byProgram[r.ProgramID] = best{r, spec}
		}
	}

	out := make([]ProgramMatch, 0, len(order))
	for _, id := range order {
		b := byProgram[id]
		out = append(out, ProgramMatch{ProgramID: id, InScope: b.rule.InScope, Rule: b.rule})
	}
	return out
}

// Candidates returns what a rule needs to possibly match input, so callers
// can load only those rules: a host equal to the input's host or to one of
// its parent domains (wildcards), and, when cidrs is true, any CIDR range.
func Candidates(input string) (hosts []string, cidrs, ok bool) {
	host, ok := hostOf(input)
	if !ok {
		return nil, false, false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return []string{host}, true, true
	}
	hosts = []string{host}
	for rest := host; ; {
		var found bool
		if _, rest, found = strings.Cut(rest, "."); !found || rest == "" {
			break
		}
		hosts = append(hosts, rest)
	}
	// A name of hundreds of labels would mean hundreds of lookups; rules
	// sit near the apex, so beyond the host itself only the parents
	// closest to it are looked up.
	if len(hosts) > MaxCandidates {
		hosts = append(hosts[:1], hosts[len(hosts)-(MaxCandidates-1):]...)
	}
	return hosts, false, true
}

// MaxCandidates is the most hosts Candidates returns for one input.
const MaxCandidates = 12

// specificity returns 0 for no match, higher for more specific matches.
func specificity(t Target, host, inPath string, addr netip.Addr, isIP bool) int {
	const exact = 1 << 20
	switch t.Kind {
	case KindDomain, KindIP:
		if t.Host == host {
			return exact
		}
	case KindURL:
		return urlSpecificity(t, host, inPath, isIP)
	case KindWildcard:
		// A '*' spans any characters, dots included, as in exports.
		if t.Host != "" && !isIP && strings.HasSuffix(host, "."+t.Host) {
			if ok, _ := path.Match(t.Value, host); ok {
				return 1 + len(t.Host)
			}
		}
	case KindCIDR:
		if isIP {
			if p, err := netip.ParsePrefix(t.Value); err == nil && p.Contains(addr) {
				return 1 + p.Bits()
			}
		}
	}
	return 0
}

// hostOf extracts a normalised host from a hostname, IP or URL.
func hostOf(input string) (string, bool) {
	s := strings.TrimSpace(input)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		s = u.Hostname()
	} else {
		s, _, _ = strings.Cut(s, "/")
		if a, err := netip.ParseAddrPort(s); err == nil {
			s = a.Addr().String()
		} else if h, _, ok := strings.Cut(s, ":"); ok && !strings.Contains(h, ":") {
			s = h
		}
	}
	if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return a.String(), true
	}
	return normaliseHost(s, false)
}

// urlSpecificity matches a URL target: its host (or host pattern), then
// its path. A path rule outranks any host rule, a longer path a shorter.
func urlSpecificity(t Target, host, inPath string, isIP bool) int {
	const exact = 1 << 20
	u, ok := parseTargetURL(t.Value)
	if !ok {
		return 0
	}
	spec := 0
	switch ruleHost := strings.ToLower(u.Hostname()); {
	case ruleHost == host:
		spec = exact
	case strings.Contains(ruleHost, "*") && !isIP && t.Host != "" && strings.HasSuffix(host, "."+t.Host):
		if ok, _ := path.Match(ruleHost, host); ok {
			spec = 1 + len(t.Host)
		}
	}
	if spec == 0 {
		return 0
	}
	rulePath := strings.TrimSuffix(u.EscapedPath(), "*")
	if rulePath == "" || rulePath == "/" {
		return spec
	}
	if !pathCovers(rulePath, inPath) {
		return 0
	}
	return spec + exact + len(rulePath)
}

// pathCovers says whether a rule's path covers in: the same path, or one
// under it ("/apps/12" covers "/apps/12/x", not "/apps/123"); a '*' in the
// rule spans anything. Case doesn't count: hosts often don't care, and
// the scope check had rather say "in scope, check it" than miss one.
func pathCovers(rule, in string) bool {
	rule, in = strings.ToLower(rule), strings.ToLower(in)
	if in == "" {
		return false // a bare host isn't covered by a rule for one of its paths
	}
	if strings.Contains(rule, "*") {
		parts := strings.Split(rule, "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		re, err := regexp.Compile("^" + strings.Join(parts, ".*"))
		return err == nil && re.MatchString(in)
	}
	if in == rule || in == strings.TrimSuffix(rule, "/") {
		return true
	}
	if !strings.HasSuffix(rule, "/") {
		rule += "/"
	}
	return strings.HasPrefix(in, rule)
}

// pathOf is the path of a URL or "host/path" input, without its query;
// "" for none or just "/".
func pathOf(input string) string {
	s := strings.TrimSpace(input)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		s = u.EscapedPath()
	} else if _, rest, ok := strings.Cut(s, "/"); ok {
		s = "/" + rest
		s, _, _ = strings.Cut(s, "?")
		s, _, _ = strings.Cut(s, "#")
	} else {
		s = ""
	}
	if s == "/" {
		return ""
	}
	return s
}
