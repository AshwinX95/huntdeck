package web

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform/intigriti"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Program text (policies, introductions) is written by programs and fetched
// from platforms, so it's untrusted. The renderer keeps goldmark's safe
// defaults (raw HTML is dropped), only turns http(s) and mailto URLs into
// links, opens them in a new tab without a referrer, and shows images as
// links so a page never loads third-party content.
var markdownRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(safeLinks{}, 100))),
)

// htmlTags are the HTML tags program text uses. They're dropped (raw HTML
// isn't rendered), except a line break, which becomes one. Anything else
// in angle brackets is a placeholder, as in "X-Bug-Bounty: <username>",
// and shows as written.
var htmlTags = map[string]bool{
	"a": true, "abbr": true, "b": true, "blockquote": true, "br": true, "center": true, "code": true, "del": true, "details": true,
	"div": true, "em": true, "font": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "hr": true,
	"i": true, "iframe": true, "img": true, "ins": true, "kbd": true, "li": true, "mark": true, "ol": true, "p": true, "pre": true,
	"s": true, "script": true, "small": true, "span": true, "strike": true, "strong": true, "style": true, "sub": true,
	"summary": true, "sup": true, "svg": true, "table": true, "tbody": true, "td": true, "th": true, "thead": true, "tr": true,
	"u": true, "ul": true, "video": true, "audio": true, "object": true, "embed": true, "form": true, "input": true, "button": true,
	"textarea": true, "select": true, "option": true, "link": true, "meta": true, "base": true, "html": true, "body": true, "head": true,
}

// tagName is the lowercased name of the tag raw starts with ("" for a
// comment, a declaration or anything else).
func tagName(raw []byte) string {
	s := strings.TrimPrefix(string(raw), "<")
	s = strings.TrimPrefix(s, "/")
	end := strings.IndexFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-'
	})
	if end < 0 {
		end = len(s)
	}
	return strings.ToLower(s[:end])
}

// tocEntry is a heading in a document, for a list of contents.
type tocEntry struct {
	ID, Text string
	Level    int
}

// markdownTop is the level program text's biggest headings get: it's
// shown under the page's h1 and a panel's h2, so a policy's own "# Scope"
// mustn't be a second h1.
const markdownTop = 3

// demoteHeadings moves doc's headings down so the biggest is markdownTop,
// keeping how they nest (no further than h6), and returns how far.
func demoteHeadings(doc ast.Node) int {
	top := 7
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			top = min(top, h.Level)
		}
		return ast.WalkContinue, nil
	})
	shift := max(0, markdownTop-top)
	if shift > 0 {
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if h, ok := n.(*ast.Heading); ok && entering {
				h.Level = min(6, h.Level+shift)
			}
			return ast.WalkContinue, nil
		})
	}
	return shift
}

// markdownDoc renders untrusted Markdown as markdown does, giving its
// headings (down to level 3 as written) IDs that start with prefix, and
// lists them at their levels as written.
func markdownDoc(src, prefix string) (template.HTML, []tocEntry) {
	source := []byte(src)
	doc := markdownRenderer.Parser().Parse(text.NewReader(source))
	shift := demoteHeadings(doc)
	var toc []tocEntry
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering || h.Level-shift > 3 {
			return ast.WalkContinue, nil
		}
		id := fmt.Sprintf("%s-%d", prefix, len(toc)+1)
		h.SetAttributeString("id", []byte(id))
		if t := strings.TrimSpace(nodeText(h, source)); t != "" {
			toc = append(toc, tocEntry{ID: id, Text: t, Level: h.Level - shift})
		}
		return ast.WalkSkipChildren, nil
	})
	var b bytes.Buffer
	if err := markdownRenderer.Renderer().Render(&b, source, doc); err != nil {
		return markdown(src), nil
	}
	return template.HTML(b.String()), toc //nolint:gosec // goldmark output with raw HTML disabled and links vetted
}

// nodeText is the plain text inside n.
func nodeText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.CodeSpan:
			for g := t.FirstChild(); g != nil; g = g.NextSibling() {
				if tx, ok := g.(*ast.Text); ok {
					b.Write(tx.Segment.Value(source))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// markdown renders untrusted Markdown to HTML that is safe to embed.
func markdown(src string) template.HTML {
	source := []byte(src)
	doc := markdownRenderer.Parser().Parse(text.NewReader(source))
	demoteHeadings(doc)
	var b bytes.Buffer
	if err := markdownRenderer.Renderer().Render(&b, source, doc); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>") //nolint:gosec // escaped above
	}
	return template.HTML(b.String()) //nolint:gosec // goldmark output with raw HTML disabled and links vetted
}

// safeLinks renders links, autolinks and images.
type safeLinks struct{}

func (safeLinks) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindRawHTML, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkSkipChildren, nil
		}
		raw := n.(*ast.RawHTML)
		var b []byte
		for i := 0; i < raw.Segments.Len(); i++ {
			seg := raw.Segments.At(i)
			b = append(b, seg.Value(src)...)
		}
		switch name := tagName(b); {
		case name == "br":
			_, _ = w.WriteString("<br>")
		case name == "" || htmlTags[name]:
			// raw HTML stays out
		default:
			_, _ = w.Write(util.EscapeHTML(b))
		}
		return ast.WalkSkipChildren, nil
	})
	r.Register(ast.KindLink, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		return renderLink(w, n.(*ast.Link).Destination, entering), nil
	})
	r.Register(ast.KindImage, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		// The alt text (the children) becomes the link text.
		return renderLink(w, n.(*ast.Image).Destination, entering), nil
	})
	r.Register(ast.KindAutoLink, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		l := n.(*ast.AutoLink)
		label := l.Label(src)
		dest := l.URL(src)
		if l.AutoLinkType == ast.AutoLinkEmail && !bytes.HasPrefix(bytes.ToLower(dest), []byte("mailto:")) {
			dest = append([]byte("mailto:"), dest...)
		}
		renderLink(w, dest, true)
		_, _ = w.Write(util.EscapeHTML(label))
		renderLink(w, dest, false)
		return ast.WalkContinue, nil
	})
}

// renderLink opens or closes an <a> for dest; unsafe destinations render
// their text only.
func renderLink(w util.BufWriter, dest []byte, entering bool) ast.WalkStatus {
	if !safeURL(string(dest)) {
		return ast.WalkContinue
	}
	if entering {
		_, _ = w.WriteString(`<a href="`)
		_, _ = w.Write(util.EscapeHTML(util.URLEscape([]byte(researcherLink(string(dest))), true)))
		_, _ = w.WriteString(`" target="_blank" rel="noopener noreferrer nofollow">`)
	} else {
		_, _ = w.WriteString(`</a>`)
	}
	return ast.WalkContinue
}

// researcherLink makes a link to an Intigriti program page one that opens
// it signed in from here (intigriti.ProgramLink): programs link each other
// at app.intigriti.com/programs/…, which shows Forbidden when followed from
// another site.
func researcherLink(u string) string {
	if l, ok := intigriti.ProgramLink(u); ok {
		return l
	}
	return u
}

func safeURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "mailto:")
}
