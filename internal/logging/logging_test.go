package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			log := New(&buf, slog.LevelInfo, format)
			log.Info("request",
				"Authorization", "Basic dXNlcjpwYXNz",
				"h1_token", "tok-123",
				"BC_PASSWORD", "hunter2",
				slog.Group("headers", "Cookie", "_session=abc"),
				"program", "example",
			)
			out := buf.String()
			for _, secret := range []string{"dXNlcjpwYXNz", "tok-123", "hunter2", "_session=abc"} {
				if strings.Contains(out, secret) {
					t.Errorf("secret %q leaked: %s", secret, out)
				}
			}
			if !strings.Contains(out, "example") {
				t.Errorf("non-sensitive value missing: %s", out)
			}
			if strings.Count(out, Redacted) != 4 {
				t.Errorf("expected 4 redactions: %s", out)
			}
		})
	}
}

func TestLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelWarn, "text")
	log.Info("hidden")
	log.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Errorf("level filtering wrong: %s", buf.String())
	}
}
