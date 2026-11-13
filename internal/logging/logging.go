// Package logging builds the application's slog logger with secret redaction.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted replaces the value of any attribute whose key looks sensitive.
const Redacted = "[REDACTED]"

// sensitiveKeyParts are matched case-insensitively against attribute keys,
// including keys nested inside groups.
var sensitiveKeyParts = []string{
	"authorization", "cookie", "password", "passwd", "secret",
	"token", "session", "otp", "apikey", "api_key", "credential",
}

// IsSensitiveKey reports whether a log attribute key should be redacted.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// New returns a logger writing text or JSON to w at the given level.
// Attributes with sensitive-looking keys are always redacted. This is a
// safety net: code should still avoid logging secrets in the first place.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Value.Kind() != slog.KindGroup && IsSensitiveKey(a.Key) {
				return slog.String(a.Key, Redacted)
			}
			return a
		},
	}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}
