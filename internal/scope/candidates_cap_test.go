package scope

import (
	"strings"
	"testing"
)

// A name of many labels is looked up as itself and its parents nearest
// the apex, never hundreds of them.
func TestCandidatesCapped(t *testing.T) {
	for _, tt := range []struct {
		in        string
		n         int
		first, at string // the input's host, and the last candidate
	}{
		{"a.b.example.com", 4, "a.b.example.com", "com"},
		{strings.Repeat("x.", 120) + "example.com", MaxCandidates, strings.Repeat("x.", 120) + "example.com", "com"},
	} {
		hosts, _, ok := Candidates(tt.in)
		if !ok || len(hosts) != tt.n || hosts[0] != tt.first || hosts[len(hosts)-1] != tt.at {
			t.Errorf("Candidates(%.30q…) = %d hosts, first %.20q, last %q", tt.in, len(hosts), hosts[0], hosts[len(hosts)-1])
		}
	}
	hosts, _, _ := Candidates(strings.Repeat("x.", 120) + "example.com")
	if hosts[len(hosts)-2] != "example.com" {
		t.Errorf("the apex isn't kept: %q", hosts[len(hosts)-2])
	}
}
