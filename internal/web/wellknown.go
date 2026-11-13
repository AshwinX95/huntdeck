package web

import (
	"net/http"
	"strings"
	"time"
)

// Files browsers and others look for at fixed paths, outside /static/.

// rootIcon serves a static icon at the path browsers ask for it by
// default: /favicon.ico, and /apple-touch-icon.png for a phone's home
// screen (with its "-precomposed" name too).
func rootIcon(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		if rasterImage(name) {
			w.Header().Set("Content-Security-Policy", imagePolicy)
		}
		http.ServeFileFS(w, r, staticFS, "static/"+name)
	}
}

// securityTxtFor is how long a security.txt says it holds (RFC 9116 asks
// for under a year); it's written afresh on every request.
const securityTxtFor = 180 * 24 * time.Hour

// securityTxt serves /.well-known/security.txt (RFC 9116) on a published
// copy with a feedback address: where to report a problem with the site.
// Anywhere else it's not found, as a HuntDeck run for oneself has nobody
// else to hear from.
func (s *server) securityTxt(w http.ResponseWriter, r *http.Request) {
	if !s.indexable() || s.FeedbackEmail == "" {
		http.NotFound(w, r)
		return
	}
	expires := time.Now().UTC().Add(securityTxtFor).Truncate(24 * time.Hour)
	var b strings.Builder
	b.WriteString("Contact: mailto:" + s.FeedbackEmail + "\n")
	b.WriteString("Expires: " + expires.Format(time.RFC3339) + "\n")
	b.WriteString("Preferred-Languages: en\n")
	b.WriteString("Canonical: " + s.PublicURL + "/.well-known/security.txt\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(b.String()))
}
