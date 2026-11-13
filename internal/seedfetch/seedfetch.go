// Package seedfetch downloads the starter seed a new install loads on its
// first start (HUNTDECK_SEED_URL): once, into an empty database, after
// which the install polls for itself and never asks again.
//
// The seed is checked against the SHA-256 published beside it (the URL
// with ".sha256" added: the hex digest, optionally followed by a file
// name, as sha256sum writes it). The publisher uploads the seed before its
// digest, so a download that lands between the two finds the old digest;
// the digest is read again once before the seed is refused.
package seedfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// MaxSeedBytes is the most a seed may be: well above today's (~35 MB
// gzipped), well below what would fill a small disk.
const MaxSeedBytes = 1 << 30

// Fetcher downloads seeds.
type Fetcher struct {
	Client    *http.Client // nil for http.DefaultClient
	UserAgent string
	MaxBytes  int64 // 0 for MaxSeedBytes
}

// Fetch downloads url's seed into a new file in dir and returns its path;
// the caller renames or removes it. Nothing is left behind on error.
func (f Fetcher) Fetch(ctx context.Context, url, dir string) (string, error) {
	want, err := f.digest(ctx, url+".sha256")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "seed-download-*.part")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	got, err := f.download(ctx, url, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && got != want {
		// Maybe the digest was read just before a new seed's went up.
		var again string
		if again, err = f.digest(ctx, url+".sha256"); err == nil && got != again {
			err = errors.New("seed: its SHA-256 doesn't match the published one")
		}
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (f Fetcher) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	c := f.Client
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res, nil
}

// digest reads the published SHA-256.
func (f Fetcher) digest(ctx context.Context, url string) (string, error) {
	res, err := f.get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("seed checksum: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("seed checksum: %w", err)
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", errors.New("seed checksum: empty")
	}
	d := strings.ToLower(fields[0])
	if _, err := hex.DecodeString(d); err != nil || len(d) != sha256.Size*2 {
		return "", errors.New("seed checksum: not a SHA-256")
	}
	return d, nil
}

// download writes url's body to w, up to the size limit, and returns its
// SHA-256.
func (f Fetcher) download(ctx context.Context, url string, w io.Writer) (string, error) {
	res, err := f.get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("seed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	limit := f.MaxBytes
	if limit <= 0 {
		limit = MaxSeedBytes
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(res.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("seed: %w", err)
	}
	if n > limit {
		return "", fmt.Errorf("seed: larger than %d MB", limit>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
