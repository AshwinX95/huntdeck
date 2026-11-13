package secretbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFile)
	if _, err := Load(path); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load without a key = %v, want ErrNoKey", err)
	}
	box, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != keySize {
		t.Fatalf("key file: %v, %v", fi, err)
	}

	const secret = "p#ss$word!"
	sealed, err := box.Seal("BC_PASSWORD", secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, secret) || !strings.HasPrefix(sealed, prefix) {
		t.Fatalf("sealed = %q", sealed)
	}
	if again, _ := box.Seal("BC_PASSWORD", secret); again == sealed {
		t.Error("sealing twice gave the same text: the nonce isn't random")
	}

	// The same key, read again, opens it; only under its own name.
	box2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := box2.Open("BC_PASSWORD", sealed); err != nil || got != secret {
		t.Fatalf("Open = %q, %v", got, err)
	}
	for _, c := range []struct{ name, sealed string }{
		{"YWH_PASSWORD", sealed},                       // moved to another setting
		{"BC_PASSWORD", sealed[:len(sealed)-2] + "AA"}, // damaged
		{"BC_PASSWORD", secret},                        // never sealed
		{"BC_PASSWORD", prefix + "!!"},                 // not base64
	} {
		if got, err := box.Open(c.name, c.sealed); !errors.Is(err, ErrOpen) || got != "" {
			t.Errorf("Open(%s, %.12q…) = %q, %v; want ErrOpen", c.name, c.sealed, got, err)
		}
	}

	// Another key can't open it.
	other, err := LoadOrCreate(filepath.Join(dir, "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open("BC_PASSWORD", sealed); !errors.Is(err, ErrOpen) {
		t.Errorf("another key opened it: %v", err)
	}
}

func TestBadKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), KeyFile)
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("a key of the wrong size was accepted")
	}
}
