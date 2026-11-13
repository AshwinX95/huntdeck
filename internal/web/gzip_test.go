package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"gzip, deflate, br, zstd", true},
		{"br;q=1.0, GZIP;q=0.5", true},
		{"gzip;q=0", false},
		{"gzip; q=0", false},
		{"deflate, br", false},
		{"x-gzip", false},
	}
	for _, tt := range tests {
		if got := acceptsGzip(tt.header); got != tt.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestCompression(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	gz := http.Header{"Accept-Encoding": {"gzip"}}
	tests := []struct {
		name, path string
		header     http.Header
		gzipped    bool
		want       string // in the (decompressed) body
	}{
		{"page", "/programs", gz, true, `class="topbar"`},
		{"stylesheet", "/static/css/12-board.css", gz, true, ".board"},
		{"script", "/static/board.js", gz, true, "huntdeck-board"},
		{"json", "/api/v1/stats", gz, true, "{"},
		{"not asked for", "/programs", nil, false, `class="topbar"`},
		{"refused", "/programs", http.Header{"Accept-Encoding": {"gzip;q=0"}}, false, `class="topbar"`},
		{"range", "/static/css/12-board.css", http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-9"}}, false, ""},
		{"not found", "/no-such-page", gz, false, "Page not found"},
	}
	for _, tt := range tests {
		res, body := e.do(c, http.MethodGet, tt.path, nil, tt.header)
		if got := res.Header.Get("Content-Encoding") == "gzip"; got != tt.gzipped {
			t.Errorf("%s: gzipped = %v, want %v", tt.name, got, tt.gzipped)
			continue
		}
		if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
			t.Errorf("%s: no Vary: Accept-Encoding", tt.name)
		}
		if tt.gzipped {
			if cl := res.Header.Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(body)) {
				t.Errorf("%s: Content-Length %s for a %d-byte compressed body", tt.name, cl, len(body))
			}
			r, err := gzip.NewReader(bytes.NewReader([]byte(body)))
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			body = string(b)
		}
		if !strings.Contains(body, tt.want) {
			t.Errorf("%s: body lacks %q", tt.name, tt.want)
		}
	}
}
