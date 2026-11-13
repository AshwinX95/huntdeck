package scope

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Verdicts for a whole input across all programs.
const (
	VerdictIn   = "in"   // in scope for at least one program
	VerdictOut  = "out"  // only programs that exclude it match
	VerdictNone = "none" // no program's scope mentions it
)

// Verdict sums up an input's matches: in scope if any program has it in
// scope.
func Verdict(matches []ProgramMatch) string {
	if len(matches) == 0 {
		return VerdictNone
	}
	for _, m := range matches {
		if m.InScope {
			return VerdictIn
		}
	}
	return VerdictOut
}

// ReadInputs reads one host, IP or URL per line, skipping blank lines,
// "#" comments and repeats, so tool output can be piped in as it is. It
// fails past max lines (0: no limit).
func ReadInputs(r io.Reader, max int) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	seen := map[string]bool{}
	var out []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || seen[line] {
			continue
		}
		if max > 0 && len(out) == max {
			return nil, fmt.Errorf("more than %d inputs", max)
		}
		seen[line] = true
		out = append(out, line)
	}
	return out, sc.Err()
}
