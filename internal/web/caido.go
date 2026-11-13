package web

import (
	"strings"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A program's scope for Caido (scope/caido.go): the file the program page's
// export menu downloads, and what it leaves out, which the menu lists so
// nothing goes missing unsaid.

// caidoExport is a program's live scope as Caido's allowlist and denylist,
// and the entries left out: those that aren't hosts (apps, hardware, …)
// and targets a host glob can't express.
func caidoExport(entries []store.EntryView) (allow, deny []string, skipped []scope.CaidoSkip) {
	var in, out []scope.Target
	var notHosts []scope.CaidoSkip
	for _, e := range entries {
		if e.RemovedAt != nil {
			continue
		}
		if len(e.Targets) == 0 {
			notHosts = append(notHosts, scope.CaidoSkip{Value: e.Identifier, InScope: e.InScope,
				Why: "not a host (" + strings.ReplaceAll(e.AssetType, "_", " ") + ")"})
			continue
		}
		if e.InScope {
			in = append(in, e.Targets...)
		} else {
			out = append(out, e.Targets...)
		}
	}
	allow, deny, skipped = scope.CaidoLists(in, out)
	return allow, deny, append(skipped, notHosts...)
}

// caidoFileName is the download's name: huntdeck-<platform>-<handle>-caido.json.
func caidoFileName(p store.ProgramRef) string {
	return fileSafe("huntdeck-"+p.Platform+"-"+p.Handle) + "-caido.json"
}

// caidoNote is the export menu's list of what Caido's file leaves out,
// the first few and how many more.
type caidoNote struct {
	Shown []scope.CaidoSkip
	More  int
}

const caidoNoteShown = 6

// CaidoSkipped is the program's targets Caido's file leaves out, for the
// export menu; nil if none.
func (d programData) CaidoSkipped() *caidoNote {
	_, _, skipped := caidoExport(d.P.Entries)
	if len(skipped) == 0 {
		return nil
	}
	n := &caidoNote{Shown: skipped[:min(len(skipped), caidoNoteShown)]}
	n.More = len(skipped) - len(n.Shown)
	return n
}
