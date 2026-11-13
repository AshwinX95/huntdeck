package seedfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestFetch(t *testing.T) {
	seed := []byte("not really a seed, but bytes all the same")
	other := []byte("a newer seed")
	tests := []struct {
		name    string
		digests []string // served in turn; the last repeats
		body    []byte
		status  int
		max     int64
		wantErr string
	}{
		{"matches", []string{sum(seed) + "  seed.db.gz\n"}, seed, 200, 0, ""},
		{"digest alone, upper case", []string{strings.ToUpper(sum(seed))}, seed, 200, 0, ""},
		{"new digest went up during the download", []string{sum(other), sum(seed)}, seed, 200, 0, ""},
		{"doesn't match", []string{sum(other)}, seed, 200, 0, "doesn't match"},
		{"not a digest", []string{"<html>"}, seed, 200, 0, "not a SHA-256"},
		{"seed missing", []string{sum(seed)}, seed, 404, 0, "HTTP 404"},
		{"too big", []string{sum(seed)}, seed, 200, 10, "larger than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ua := r.Header.Get("User-Agent"); ua != "HuntDeck/test" {
					t.Errorf("User-Agent %q", ua)
				}
				switch r.URL.Path {
				case "/seed.db.gz.sha256":
					i := min(int(asked.Add(1))-1, len(tt.digests)-1)
					_, _ = w.Write([]byte(tt.digests[i]))
				case "/seed.db.gz":
					w.WriteHeader(tt.status)
					_, _ = w.Write(tt.body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			dir := t.TempDir()
			f := Fetcher{Client: srv.Client(), UserAgent: "HuntDeck/test", MaxBytes: tt.max}
			path, err := f.Fetch(context.Background(), srv.URL+"/seed.db.gz", dir)
			left, _ := filepath.Glob(filepath.Join(dir, "*"))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if len(left) != 0 {
					t.Errorf("left behind %v", left)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != string(tt.body) || len(left) != 1 {
				t.Errorf("downloaded %q, files %v", got, left)
			}
		})
	}
}
