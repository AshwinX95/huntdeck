package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHealthURL(t *testing.T) {
	tests := []struct{ listen, want string }{
		{"0.0.0.0:8080", "http://127.0.0.1:8080/healthz"},
		{":8080", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "http://[::1]:8080/healthz"},
		{"127.0.0.1:9000", "http://127.0.0.1:9000/healthz"},
		{"10.0.0.5:80", "http://10.0.0.5:80/healthz"},
	}
	for _, tt := range tests {
		got, err := healthURL(tt.listen)
		if err != nil || got != tt.want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", tt.listen, got, err, tt.want)
		}
	}
	if _, err := healthURL("8080"); err == nil {
		t.Error("healthURL(\"8080\") should fail")
	}
}

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		args     []string
		wantCode int
		wantOut  string
	}{
		{nil, 2, "Usage:"},
		{[]string{"nope"}, 2, `unknown command "nope"`},
		{[]string{"version"}, 0, "huntdeck dev"},
		{[]string{"help"}, 0, "Commands:"},
		{[]string{"serve", "extra"}, 2, "unexpected arguments"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		code := run(tt.args, &out, &out)
		if code != tt.wantCode || !strings.Contains(out.String(), tt.wantOut) {
			t.Errorf("run(%v) = %d, %q; want %d containing %q", tt.args, code, out.String(), tt.wantCode, tt.wantOut)
		}
	}
}
