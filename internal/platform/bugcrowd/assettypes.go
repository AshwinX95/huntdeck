package bugcrowd

import (
	"net/netip"
	"strings"

	"github.com/mdpsec/huntdeck/internal/scope"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// assetType maps Bugcrowd target categories to normalised asset types.
// "website" covers wildcards, hosts and URLs alike, so it looks at the value,
// and so does anything that would be "other": Bugcrowd files plenty of
// plain wildcards and hosts under "other" (and moves them there).
func assetType(category, ident string) string {
	if t := categoryType(category, ident); t != scope.AssetOther {
		return t
	}
	return labelOther(ident)
}

// labelOther types an identifier that's exactly one wildcard, host, URL, IP
// or range and nothing else ("*.example.com", "https://app.example.com/login"):
// its targets all share one host and one of them is the whole identifier
// (a URL also yields its host). Prose, lists and anything that isn't a real
// hostname stay "other". Migration 0027 applies the same rule to entries
// stored before it.
func labelOther(ident string) string {
	ts := scope.Targets(scope.AssetOther, ident)
	if len(ts) == 0 {
		return scope.AssetOther
	}
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(ident)), `\*`, "*")
	bare, ok := strings.CutPrefix(s, "https://")
	if !ok {
		bare = strings.TrimPrefix(s, "http://")
	}
	bare = strings.TrimSuffix(strings.TrimSuffix(bare, "/*"), "/")
	kind := ""
	for _, t := range ts {
		if t.Host != ts[0].Host {
			return scope.AssetOther
		}
		switch v := strings.ToLower(t.Value); {
		case v == s:
			kind = t.Kind
		case v == bare && kind == "":
			kind = t.Kind
		}
	}
	if kind == "" {
		return scope.AssetOther
	}
	return kind // the kinds share the asset types' names
}

// categoryType maps a category as Bugcrowd gives it.
func categoryType(category, ident string) string {
	switch strings.ToLower(category) {
	case "website":
		return scope.WebType(ident)
	case "api":
		return scope.AssetAPI
	case "ip_address", "network":
		if _, err := netip.ParsePrefix(ident); err == nil {
			return scope.AssetCIDR
		}
		if _, err := netip.ParseAddr(ident); err == nil {
			return scope.AssetIP
		}
		if category == "ip_address" {
			return scope.AssetIP
		}
		return scope.AssetOther
	case "android":
		return scope.AssetAndroid
	case "ios":
		return scope.AssetIOS
	case "hardware", "iot":
		return scope.AssetHardware
	default:
		return scope.AssetOther
	}
}
