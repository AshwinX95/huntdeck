package scope

import (
	"strings"
	"testing"
)

func TestReadInputs(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		want     string
		err      bool
	}{
		{"tool output", "a.example.com\n\n# from subfinder\n  b.example.com  \r\na.example.com\n", 0, "a.example.com,b.example.com", false},
		{"at the limit", "a\nb\nb\n", 2, "a,b", false},
		{"over the limit", "a\nb\nc\n", 2, "", true},
		{"empty", "", 0, "", false},
	}
	for _, tt := range tests {
		got, err := ReadInputs(strings.NewReader(tt.in), tt.max)
		if (err != nil) != tt.err || strings.Join(got, ",") != tt.want {
			t.Errorf("%s: ReadInputs = %q, %v", tt.name, got, err)
		}
	}
}

func TestVerdict(t *testing.T) {
	in, out := ProgramMatch{ProgramID: 1, InScope: true}, ProgramMatch{ProgramID: 2}
	tests := []struct {
		matches []ProgramMatch
		want    string
	}{
		{nil, VerdictNone},
		{[]ProgramMatch{out}, VerdictOut},
		{[]ProgramMatch{out, in}, VerdictIn},
	}
	for _, tt := range tests {
		if got := Verdict(tt.matches); got != tt.want {
			t.Errorf("Verdict(%v) = %s, want %s", tt.matches, got, tt.want)
		}
	}
}
