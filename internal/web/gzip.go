package web

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// compress gzips text responses (pages, CSS, scripts, JSON and plain-text
// exports) for clients that accept it: a page is about an eighth of its size
// compressed, so it arrives, and the hunt board appears, much sooner on a
// slow link. Pages carry no secrets or tokens for a compression side channel
// to recover.
//
// Writers are reused: each holds about a megabyte of compression state.
func compress(next http.Handler) http.Handler {
	pool := &sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		// A websocket (noVNC's) takes over the connection: nothing to compress.
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || r.Header.Get("Upgrade") != "" ||
			!acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w, pool: pool}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return strings.ReplaceAll(strings.TrimSpace(params), " ", "") != "q=0"
		}
	}
	return false
}

// compressible reports whether a Content-Type is text worth compressing.
func compressible(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mt {
	case "application/json", "application/javascript", "text/javascript", "image/svg+xml":
		return true
	}
	return strings.HasPrefix(mt, "text/")
}

// gzipWriter decides at the first write whether to compress: only full (200)
// responses of a text type that aren't already encoded.
type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	pool    *sync.Pool
	decided bool
}

func (g *gzipWriter) WriteHeader(status int) {
	if !g.decided {
		g.decided = true
		h := g.Header()
		if status == http.StatusOK && h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
			h.Set("Content-Encoding", "gzip")
			h.Del("Content-Length")
			g.gz = g.pool.Get().(*gzip.Writer)
			g.gz.Reset(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(io.Discard) // let go of the response
		g.pool.Put(g.gz)
	}
}
