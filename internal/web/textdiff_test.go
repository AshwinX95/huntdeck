package web

import (
	"strings"
	"testing"
)

// render writes a diff compactly: one line per row, words marked [-x-] and
// {+x+}, skips as "…N".
func render(lines []diffLine) string {
	var b strings.Builder
	for _, l := range lines {
		switch l.Op {
		case opSkip:
			b.WriteString("…" + strings.Repeat("·", l.Skip) + "\n")
			continue
		case opIns:
			b.WriteString("+ ")
		case opDel:
			b.WriteString("- ")
		default:
			b.WriteString("  ")
		}
		if l.Parts == nil {
			b.WriteString(l.Text)
		}
		for _, p := range l.Parts {
			switch p.Op {
			case opIns:
				b.WriteString("{+" + p.Text + "+}")
			case opDel:
				b.WriteString("[-" + p.Text + "-]")
			default:
				b.WriteString(p.Text)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestTextDiff(t *testing.T) {
	long := "a\nb\nc\nd\ne\nf\ng\nh"
	tests := []struct {
		name, a, b, want string
		added, removed   int
	}{
		{"identical", "x\ny", "x\r\ny  ", "…··\n", 0, 0},
		{"word edited", "Be nice.\nNo DoS.", "Be nicer.\nNo DoS.",
			"- Be [-nice.-]\n+ Be {+nicer.+}\n  No DoS.\n", 1, 1},
		{"line added", "one\ntwo", "one\nnew\ntwo", "  one\n+ new\n  two\n", 1, 0},
		{"lines replaced unevenly", "a\nold1\nold2\nz", "a\nnew\nz", "  a\n- old1\n- old2\n+ new\n  z\n", 1, 2},
		{"far context collapsed", long, strings.Replace(long, "e", "E", 1),
			"…··\n  c\n  d\n- [-e-]\n+ {+E+}\n  f\n  g\n…·\n", 1, 1},
		{"from nothing", "", "new", "+ new\n", 1, 0},
	}
	for _, tt := range tests {
		lines, added, removed := textDiff(tt.a, tt.b)
		if got := render(lines); got != tt.want || added != tt.added || removed != tt.removed {
			t.Errorf("%s:\n got (+%d −%d)\n%s\nwant (+%d −%d)\n%s", tt.name, added, removed, got, tt.added, tt.removed, tt.want)
		}
	}
}

// A text too large to compare line by line still diffs, as a block.
func TestTextDiffHugeChange(t *testing.T) {
	var a, b []string
	for i := range 2000 {
		a = append(a, "old "+strings.Repeat("x", i%7))
		b = append(b, "new "+strings.Repeat("y", i%5))
	}
	_, added, removed := textDiff(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if added != 2000 || removed != 2000 {
		t.Errorf("+%d −%d", added, removed)
	}
}

func TestRewardsDiff(t *testing.T) {
	old := `[{"scope":"*.acme.example","low":{},"medium":{},"high":{"max":2000},"critical":{"max":5000}},
		{"scope":"legacy.acme.example","low":{"max":100},"medium":{},"high":{},"critical":{}}]`
	cur := `[{"scope":"*.acme.example","low":{},"medium":{},"high":{"max":2000},"critical":{"min":5000,"max":10000}},
		{"scope":"api.acme.example","low":{},"medium":{},"high":{},"critical":{"max":20000}}]`
	var got []string
	for _, c := range rewardsDiff(old, cur, "USD") {
		got = append(got, describeReward(c))
	}
	want := []string{
		"*.acme.example critical: $5k → $5k – $10k",
		"new row api.acme.example: critical $20k",
		"row removed legacy.acme.example",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if d := rewardsDiff(old, old, "EUR"); len(d) != 0 {
		t.Errorf("same table: %v", d)
	}
}
