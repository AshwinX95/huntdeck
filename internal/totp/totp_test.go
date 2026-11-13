package totp

import (
	"encoding/base32"
	"errors"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors for SHA-1 (8 digits, key "12345678901234567890").
func TestRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))
	tests := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, tt := range tests {
		got, err := code(secret, Step(time.Unix(tt.unix, 0)), 8)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("t=%d: got %s, want %s", tt.unix, got, tt.want)
		}
	}
}

func TestCodeAcceptsMessySecrets(t *testing.T) {
	at := time.Unix(1111111109, 0)
	want, err := Code("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", at)
	if err != nil {
		t.Fatal(err)
	}
	if want != "081804" {
		t.Fatalf("six-digit code = %s, want 081804", want)
	}
	for _, s := range []string{"gezd gnbv gy3t qojq gezd gnbv gy3t qojq", "GEZD-GNBV-GY3T-QOJQ-GEZD-GNBV-GY3T-QOJQ", " GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ==== "} {
		got, err := Code(s, at)
		if err != nil || got != want {
			t.Errorf("Code(%q) = %q, %v; want %q", s, got, err, want)
		}
	}
}

func TestCodeRejectsBadSecrets(t *testing.T) {
	for _, s := range []string{"", "   ", "not base32!", "01189998819991197253"} {
		if _, err := Code(s, time.Now()); !errors.Is(err, ErrBadSecret) {
			t.Errorf("Code(%q) error = %v, want ErrBadSecret", s, err)
		}
	}
}

func TestSteps(t *testing.T) {
	at := time.Unix(1_000_000_015, 0)
	if Step(at) != 33333333 {
		t.Fatalf("Step = %d", Step(at))
	}
	if got := NextStep(at); !got.Equal(time.Unix(1_000_000_020, 0)) {
		t.Fatalf("NextStep = %v", got)
	}
}
