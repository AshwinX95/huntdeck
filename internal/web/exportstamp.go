package web

import (
	"bytes"
	"net/http"
	"time"
)

// Exports say when they were made: every target list, scope check list
// and scope file answers with an X-HuntDeck-Generated-At header, and with
// comment=1 also starts with a comment line, where its format has one:
// "# generated_at=2026-01-02T15:04:05Z" in plain text (a line pipes can
// drop with grep -v '^#'), an XML comment in ZAP's context. JSON has no
// comments, so Burp's and Caido's files keep the header only. Without
// comment=1 the body is as it always was.

// exportStamp sets the header and says whether to write the comment line,
// and when.
func exportStamp(w http.ResponseWriter, r *http.Request) (comment bool, at string) {
	at = time.Now().UTC().Format(time.RFC3339)
	w.Header().Set("X-HuntDeck-Generated-At", at)
	return isTrue(r.URL.Query().Get("comment")), at
}

// textComment is the plain-text comment line, or "" without comment=1.
func textComment(w http.ResponseWriter, r *http.Request) string {
	if comment, at := exportStamp(w, r); comment {
		return "# generated_at=" + at + "\n"
	}
	return ""
}

// xmlComment puts the comment after body's XML declaration, with
// comment=1.
func xmlComment(w http.ResponseWriter, r *http.Request, body []byte) []byte {
	comment, at := exportStamp(w, r)
	if !comment {
		return body
	}
	line := []byte("<!-- generated_at=" + at + " -->\n")
	if i := bytes.IndexByte(body, '\n'); i >= 0 && bytes.HasPrefix(body, []byte("<?xml")) {
		return append(append(append([]byte{}, body[:i+1]...), line...), body[i+1:]...)
	}
	return append(line, body...)
}
