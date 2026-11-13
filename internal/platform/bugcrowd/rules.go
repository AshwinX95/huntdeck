package bugcrowd

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// detail is the brief's rules (its overview, target notes and additional
// information, as Markdown), its bounty table (one row per in-scope target
// group, P1 as critical down to P4 as low; P5 is unpaid), and its safe
// harbor, collaboration and coordinated disclosure flags and when the
// engagement started.
func (vd versionDoc) detail() platform.ScopeDetail {
	b := vd.Data.Brief
	var parts []string
	for _, h := range []string{b.Description, b.TargetsOverview, b.AdditionalInformation} {
		if md := htmlToMarkdown(h); md != "" {
			parts = append(parts, md)
		}
	}
	d := platform.ScopeDetail{Policy: strings.Join(parts, "\n\n"), Rewards: []platform.RewardRow{}}
	for _, g := range vd.Data.Scope {
		r := g.RewardRange
		if !g.InScope || r == nil {
			continue
		}
		row := platform.RewardRow{Scope: strings.TrimSpace(g.Name), Critical: cents(r.P1Min, r.P1),
			High: cents(r.P2Min, r.P2), Medium: cents(r.P3Min, r.P3), Low: cents(r.P4Min, r.P4)}
		if row.Critical.Max > 0 || row.High.Max > 0 || row.Medium.Max > 0 || row.Low.Max > 0 {
			d.Rewards = append(d.Rewards, row)
		}
	}
	f := platform.Facts{Collaboration: vd.CollaborationEnabled, Disclosure: vd.CoordinatedDisclosure, StartedAt: vd.Data.Engagement.StartsAt}
	if sh := b.SafeHarbor; sh != nil {
		f.SafeHarbor = strings.ToLower(strings.TrimSpace(sh.Status))
	}
	if f != (platform.Facts{}) {
		d.Facts = &f
	}
	d.State = engagementState(vd.Data.Engagement.State)
	if vd.Participation != "" {
		d.Requirements = &platform.Requirements{
			Application: strings.Contains(strings.ToLower(vd.Participation), "appl"),
			IDVerified:  vd.ShowIDAlert != nil && *vd.ShowIDAlert,
		}
	}
	return d
}

// engagementState maps a brief's engagement state to a program state: ""
// for none, or one it doesn't know.
func engagementState(s string) string {
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "paused"):
		return platform.StatePaused
	case s == "in_progress":
		return platform.StateOpen
	case strings.Contains(s, "ended"), strings.Contains(s, "finished"), strings.Contains(s, "completed"),
		strings.Contains(s, "closed"), strings.Contains(s, "archived"):
		return platform.StateClosed
	}
	return ""
}

func cents(lo, hi *int) platform.RewardRange {
	var r platform.RewardRange
	if hi != nil && *hi > 0 {
		r.Max = float64(*hi) / 100
	}
	if lo != nil && *lo > 0 && float64(*lo)/100 < r.Max {
		r.Min = float64(*lo) / 100
	}
	return r
}

var (
	blankLines = regexp.MustCompile(`\n{3,}`)
	spaces     = regexp.MustCompile(`[ \t\r\n\f]+`)
)

// htmlToMarkdown turns a brief's HTML into Markdown close enough to read
// and to diff: headings, paragraphs, lists, links, emphasis and code.
// Anything else keeps its text.
func htmlToMarkdown(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var out strings.Builder
	var lists []string // "*" or "1." per open list
	var hrefs []string // per open link; "" for links not kept
	pre, skip := 0, 0
	block := func(sep string) {
		if out.Len() > 0 {
			out.WriteString(sep)
		}
	}
	newline := func() {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
	}
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		t := z.Token()
		start := tt == html.StartTagToken || tt == html.SelfClosingTagToken
		switch tt {
		case html.TextToken:
			if skip > 0 {
				continue
			}
			text := t.Data
			if pre == 0 {
				text = spaces.ReplaceAllString(text, " ")
				if strings.HasSuffix(out.String(), "\n") || out.Len() == 0 {
					text = strings.TrimLeft(text, " ")
				}
			}
			out.WriteString(text)
			continue
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
		default:
			continue
		}
		switch name := t.Data; name {
		case "script", "style":
			if start {
				skip++
			} else if skip > 0 {
				skip--
			}
		case "h1", "h2", "h3", "h4", "h5", "h6":
			block("\n\n")
			if start {
				out.WriteString(strings.Repeat("#", int(name[1]-'0')) + " ")
			}
		case "p", "div", "table", "blockquote", "section":
			block("\n\n")
		case "tr", "br":
			block("\n")
		case "td", "th":
			if !start {
				out.WriteString(" | ")
			}
		case "hr":
			block("\n\n---\n\n")
		case "ul", "ol":
			// Nested lists start on the next line; outer ones are blocks.
			if start {
				if len(lists) == 0 {
					block("\n\n")
				} else {
					newline()
				}
				lists = append(lists, map[string]string{"ul": "*", "ol": "1."}[name])
			} else if len(lists) > 0 {
				if lists = lists[:len(lists)-1]; len(lists) == 0 {
					block("\n\n")
				}
			}
		case "li":
			if start {
				newline()
				marker := "*"
				if len(lists) > 0 {
					marker = lists[len(lists)-1]
				}
				out.WriteString(strings.Repeat("  ", max(0, len(lists)-1)) + marker + " ")
			}
		case "pre":
			if start {
				pre++
				block("\n\n")
				out.WriteString("```\n")
			} else if pre > 0 {
				pre--
				out.WriteString("\n```\n\n")
			}
		case "code":
			if pre == 0 {
				out.WriteString("`")
			}
		case "strong", "b":
			out.WriteString("**")
		case "em", "i":
			out.WriteString("*")
		case "a":
			if start {
				href := ""
				for _, a := range t.Attr {
					if a.Key == "href" && (strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "mailto:")) {
						href = a.Val
					}
				}
				hrefs = append(hrefs, href)
				if href != "" {
					out.WriteString("[")
				}
			} else if len(hrefs) > 0 {
				if href := hrefs[len(hrefs)-1]; href != "" {
					out.WriteString("](" + href + ")")
				}
				hrefs = hrefs[:len(hrefs)-1]
			}
		}
	}
	lines := strings.Split(out.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
