package web

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/store"
)

// reqChip is one requirement, shown as a chip in lists and spelled out on
// program pages.
type reqChip struct{ Label, Title string }

// requires lists a program's requirements for display.
func requires(platformName string, r store.ProgramRequirements) []reqChip {
	var out []reqChip
	// Most programs ask for a Signal of 0 or less, which only hackers with
	// a negative Signal miss, so those aren't shown.
	if r.MinSignal != nil && *r.MinSignal > 0 {
		out = append(out, reqChip{
			Label: "Signal " + strconv.FormatFloat(*r.MinSignal, 'f', 1, 64),
			Title: fmt.Sprintf("%.1f or higher past-year Signal on %s; below it you get trial reports only",
				*r.MinSignal, platformLabel(platformName)),
		})
	}
	if r.IDVerified {
		out = append(out, reqChip{Label: "ID check", Title: "Identity-verified hackers only (ID verification on " + platformLabel(platformName) + ")"})
	}
	if r.Cleared {
		out = append(out, reqChip{Label: "Cleared", Title: "Background-cleared hackers only"})
	}
	if r.EEA {
		out = append(out, reqChip{Label: "EEA", Title: "Residents of the European Economic Area only"})
	}
	if r.Application {
		out = append(out, reqChip{Label: "Apply", Title: "Researchers apply and must be accepted before testing"})
	}
	if r.Terms {
		out = append(out, reqChip{Label: "Terms", Title: "You must accept the program's own terms and conditions on " + platformLabel(platformName) + " first"})
	}
	if r.TwoFactor {
		out = append(out, reqChip{Label: "2FA", Title: "Your " + platformLabel(platformName) + " account must have two-factor authentication on"})
	}
	return out
}

// noteChips are the Programs list's Notes for a program, on one line so
// every row keeps its height: the first chip, then "+N" for the rest,
// named in its tooltip.
type noteChips struct {
	First     *reqChip
	More      int
	MoreTitle string
}

// notes fits a program's requirements and test credentials to the Notes
// column.
func notes(reqs []reqChip, creds bool) noteChips {
	all := slices.Clone(reqs)
	if creds {
		all = append(all, reqChip{Label: "creds", Title: "Test credentials available from the platform"})
	}
	var n noteChips
	if len(all) == 0 {
		return n
	}
	n.First, n.More = &all[0], len(all)-1
	var rest []string
	for _, c := range all[1:] {
		rest = append(rest, c.Label+": "+c.Title)
	}
	n.MoreTitle = strings.Join(rest, "\n")
	return n
}
