package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"
)

// staticAssets are what the static handler serves besides the files
// themselves.
type staticAssets struct {
	// Version hashes every static file. Links carry it (?v=), so a browser
	// can keep what it fetched until a build changes something.
	Version string
	// Styles are the stylesheets, static/css/*.css, joined in their names'
	// order (the number prefix sets the cascade) into styles.css: one
	// request for a page's styles instead of one per file.
	Styles []byte
}

// stylesName is where the joined stylesheets are served.
const stylesName = "styles.css"

func loadStatic() (staticAssets, error) {
	var a staticAssets
	h := sha256.New()
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(staticFS, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(b)
		return nil
	})
	if err != nil {
		return a, err
	}
	a.Version = hex.EncodeToString(h.Sum(nil))[:12]
	styles, err := fs.Glob(staticFS, "static/css/*.css")
	if err != nil {
		return a, err
	}
	sort.Strings(styles)
	var buf bytes.Buffer
	for _, f := range styles {
		b, err := fs.ReadFile(staticFS, f)
		if err != nil {
			return a, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	a.Styles = buf.Bytes()
	return a, nil
}

// staticHandler serves static/ and styles.css. Asked for at the current
// version they can be cached for a year; otherwise for an hour.
func staticHandler(a staticAssets) http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embedded at build time; can't fail at runtime
	}
	fileServer := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == a.Version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		if r.URL.Path == "/static/"+stylesName {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			http.ServeContent(w, r, stylesName, time.Time{}, bytes.NewReader(a.Styles))
			return
		}
		if rasterImage(r.URL.Path) {
			w.Header().Set("Content-Security-Policy", imagePolicy)
		}
		if strings.HasSuffix(r.URL.Path, "/") { // files only: no listing of what's there
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
