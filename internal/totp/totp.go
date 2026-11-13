// Package totp generates time-based one-time passwords (RFC 6238), the
// six-digit codes authenticator apps show, for platforms whose login asks
// for one.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 authenticator apps use HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Period is the lifetime of one code.
const Period = 30 * time.Second

// ErrBadSecret means the secret isn't valid base32.
var ErrBadSecret = errors.New("totp: secret is not valid base32 (it should look like JBSWY3DPEHPK3PXP)")

// Code returns the six-digit code for secret at time t. The secret is the
// base32 key shown when setting up an authenticator app; spaces, dashes,
// lowercase and missing padding are accepted.
func Code(secret string, t time.Time) (string, error) {
	return code(secret, Step(t), 6)
}

// Step returns the 30-second time step t falls in. Codes are only accepted
// once, so callers can compare steps to avoid reusing one.
func Step(t time.Time) int64 { return t.Unix() / int64(Period/time.Second) }

// NextStep returns when the step after t's begins.
func NextStep(t time.Time) time.Time {
	return time.Unix((Step(t)+1)*int64(Period/time.Second), 0)
}

func code(secret string, step int64, digits int) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // Unix steps are positive
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, n%mod), nil
}

func decode(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(secret)))
	s = strings.TrimRight(s, "=")
	if s == "" {
		return nil, ErrBadSecret
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil || len(key) == 0 {
		return nil, ErrBadSecret
	}
	return key, nil
}
