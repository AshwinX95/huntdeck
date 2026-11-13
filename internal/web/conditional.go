package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Conditional requests for the API's program, list and match answers: each
// carries an ETag (a hash of the body) and a Last-Modified (when this
// address first gave this body), and a request whose If-None-Match names
// the ETag, or whose If-Modified-Since is no earlier than Last-Modified,
// gets 304 Not Modified and no body. If-None-Match wins when both are sent
// (RFC 9110 §13.2.2). The ETag is weak: compression changes the bytes sent,
// not what they say.
//
// Last-Modified is remembered per address in memory, so after a restart an
// unchanged answer dates from the restart: an If-Modified-Since client
// downloads it once more, and an ETag client doesn't notice.
//
// A read-only copy (the demo) is left as it was: its answers are cached
// in front of it, and its limits apply as before.

// versionMemo is when each address last started giving the body it gives
// now, for Last-Modified; at most versionMemoMax addresses are kept.
type versionMemo struct {
	mu sync.Mutex
	m  map[string]memoEntry
}

type memoEntry struct {
	etag string
	at   time.Time
}

const versionMemoMax = 4096

// seen returns when key started giving etag: now, unless it gave it last
// time too.
func (v *versionMemo) seen(key, etag string, now time.Time) time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e, ok := v.m[key]; ok && e.etag == etag {
		return e.at
	}
	if v.m == nil {
		v.m = map[string]memoEntry{}
	}
	if len(v.m) >= versionMemoMax {
		for k := range v.m { // forget some: they'll date from their next answer
			delete(v.m, k)
			if len(v.m) < versionMemoMax*3/4 {
				break
			}
		}
	}
	at := now.UTC().Truncate(time.Second) // HTTP dates have seconds
	v.m[key] = memoEntry{etag, at}
	return at
}

// bufferedResponse holds an answer until it's known whether to send it.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// conditional answers GET and HEAD requests to h as above.
func (s *server) conditional(h http.HandlerFunc) http.HandlerFunc {
	if s.ReadOnly {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		buf := &bufferedResponse{header: http.Header{}}
		h(buf, r)
		if buf.status == 0 {
			buf.status = http.StatusOK
		}
		for k, v := range buf.header {
			w.Header()[k] = v
		}
		if buf.status != http.StatusOK {
			w.WriteHeader(buf.status)
			_, _ = w.Write(buf.body.Bytes())
			return
		}
		sum := sha256.Sum256(buf.body.Bytes())
		etag := `W/"` + hex.EncodeToString(sum[:16]) + `"`
		mod := s.versions.seen(r.URL.RequestURI(), etag, time.Now())
		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Last-Modified", mod.Format(http.TimeFormat))
		h.Set("Cache-Control", "no-cache") // may be kept, but asked about each time
		if notModified(r, etag, mod) {
			h.Del("Content-Type")
			h.Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(buf.body.Bytes())
		}
	}
}

// notModified says whether r's conditions find its copy current.
func notModified(r *http.Request, etag string, mod time.Time) bool {
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		for _, t := range strings.Split(inm, ",") {
			t = strings.TrimSpace(t)
			if t == "*" || strings.TrimPrefix(t, "W/") == strings.TrimPrefix(etag, "W/") {
				return true
			}
		}
		return false
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil && !mod.After(t) {
			return true
		}
	}
	return false
}
