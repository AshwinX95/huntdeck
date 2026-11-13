package web

import (
	"regexp"
	"strings"
)

// Diff operations.
const (
	opSame = "same"
	opDel  = "del"
	opIns  = "ins"
	opSkip = "skip" // a run of unchanged lines left out
)

// diffLine is one line of a rendered diff. Parts, when set, mark the words
// that changed within a line that was edited rather than replaced.
type diffLine struct {
	Op    string
	Text  string
	Parts []diffPart
	Skip  int // opSkip: how many unchanged lines
}

type diffPart struct {
	Op   string // opSame, opDel or opIns
	Text string
}

// diffContext is how many unchanged lines are kept around each change.
const diffContext = 2

// maxDiffCells bounds the comparison table: past it, the changed middle of
// the text is shown as removed then added instead of line by line.
const maxDiffCells = 1 << 20

// textDiff compares two texts line by line.
func textDiff(a, b string) (lines []diffLine, added, removed int) {
	ops := diffOps(splitLines(a), splitLines(b))
	for _, o := range ops {
		switch o.Op {
		case opIns:
			added++
		case opDel:
			removed++
		}
	}
	return collapse(withWords(ops)), added, removed
}

func splitLines(s string) []string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return lines
}

// diffOps returns the edit script turning a into b: unchanged items kept,
// the rest deleted or inserted, via a longest common subsequence.
func diffOps(a, b []string) []diffPart {
	var head, tail []diffPart
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		head = append(head, diffPart{opSame, a[0]})
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		tail = append(tail, diffPart{opSame, a[len(a)-1]})
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	out := head
	if (len(a)+1)*(len(b)+1) > maxDiffCells {
		for _, s := range a {
			out = append(out, diffPart{opDel, s})
		}
		for _, s := range b {
			out = append(out, diffPart{opIns, s})
		}
	} else {
		out = append(out, lcsOps(a, b)...)
	}
	for i := len(tail) - 1; i >= 0; i-- {
		out = append(out, tail[i])
	}
	return out
}

func lcsOps(a, b []string) []diffPart {
	// n[i][j] is the LCS length of a[i:] and b[j:].
	w := len(b) + 1
	n := make([]int32, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				n[i*w+j] = n[(i+1)*w+j+1] + 1
			} else {
				n[i*w+j] = max(n[(i+1)*w+j], n[i*w+j+1])
			}
		}
	}
	var out []diffPart
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, diffPart{opSame, a[i]})
			i++
			j++
		case n[(i+1)*w+j] >= n[i*w+j+1]:
			out = append(out, diffPart{opDel, a[i]})
			i++
		default:
			out = append(out, diffPart{opIns, b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, diffPart{opDel, a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, diffPart{opIns, b[j]})
	}
	return out
}

// withWords turns the line script into diff lines. Where a run of removed
// lines is followed by as many added ones, each pair is an edit, and the
// words that changed are marked.
func withWords(ops []diffPart) []diffLine {
	var out []diffLine
	for i := 0; i < len(ops); {
		if ops[i].Op != opDel {
			out = append(out, diffLine{Op: ops[i].Op, Text: ops[i].Text})
			i++
			continue
		}
		d := i
		for d < len(ops) && ops[d].Op == opDel {
			d++
		}
		in := d
		for in < len(ops) && ops[in].Op == opIns {
			in++
		}
		dels, ins := ops[i:d], ops[d:in]
		if len(dels) != len(ins) {
			for _, o := range ops[i:in] {
				out = append(out, diffLine{Op: o.Op, Text: o.Text})
			}
			i = in
			continue
		}
		for k := range dels {
			words := diffOps(wordsOf(dels[k].Text), wordsOf(ins[k].Text))
			out = append(out, diffLine{Op: opDel, Text: dels[k].Text, Parts: side(words, opIns)},
				diffLine{Op: opIns, Text: ins[k].Text, Parts: side(words, opDel)})
		}
		i = in
	}
	return out
}

var wordRe = regexp.MustCompile(`\s+|[\p{L}\p{N}_$.,%-]+|.`)

func wordsOf(s string) []string { return wordRe.FindAllString(s, -1) }

// side is one line's half of a word diff: everything but the other side's
// words, merged into runs.
func side(words []diffPart, other string) []diffPart {
	var out []diffPart
	for _, w := range words {
		if w.Op == other {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Op == w.Op {
			out[n-1].Text += w.Text
			continue
		}
		out = append(out, w)
	}
	return out
}

// collapse keeps diffContext unchanged lines around each change and
// replaces longer unchanged runs with a skip marker.
func collapse(lines []diffLine) []diffLine {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Op == opSame {
			continue
		}
		for j := max(0, i-diffContext); j <= min(len(lines)-1, i+diffContext); j++ {
			keep[j] = true
		}
	}
	var out []diffLine
	for i := 0; i < len(lines); {
		if keep[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && !keep[j] {
			j++
		}
		out = append(out, diffLine{Op: opSkip, Skip: j - i})
		i = j
	}
	return out
}
