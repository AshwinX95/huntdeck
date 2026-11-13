package web

import (
	"slices"
	"strings"
)

// The Programs list narrows to one platform or several at once:
// ?platform=hackerone,bugcrowd. Each platform in the rail turns itself on
// or off, keeping the others, and a view keeps the platforms chosen.

// platformsParam reads ?platform=: names (or short names, "h1") separated
// by commas, each once.
func platformsParam(v string) []string {
	var out []string
	for _, name := range strings.Split(v, ",") {
		if p := canonicalPlatform(name); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// platformParam is the chosen platforms as ?platform= gives them, in the
// rail's order (any it doesn't list after), so a choice has one address.
func (d programsData) platformParam() string {
	var out []string
	for _, p := range d.Platforms {
		if slices.Contains(d.Filter.Platforms, p) {
			out = append(out, p)
		}
	}
	for _, p := range d.Filter.Platforms {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// platformOptions are the rail's platform choices: each turns itself on or
// off, keeping the others chosen.
func (d programsData) platformOptions() []railOption {
	chosen := d.Filter.Platforms
	var out []railOption
	for _, p := range d.Platforms {
		on := slices.Contains(chosen, p)
		var next []string
		for _, q := range d.Platforms { // in the rail's order, so a list has one address
			if q == p && !on || q != p && slices.Contains(chosen, q) {
				next = append(next, q)
			}
		}
		out = append(out, railOption{Label: platformLabel(p), Platform: p, Count: d.Facets.Platforms[p], Counted: true, On: on,
			URL: d.queryWith("platform", strings.Join(next, ","))})
	}
	return out
}
