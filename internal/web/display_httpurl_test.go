package web

import "testing"

func TestHTTPURL(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"https://hackerone.com/reports/1", "https://hackerone.com/reports/1"},
		{" http://example.com/x ", "http://example.com/x"},
		{"javascript:alert(1)", ""},
		{"JAVASCRIPT:alert(1)", ""},
		{"data:text/html,x", ""},
		{"//example.com/x", ""},
		{"/relative", ""},
		{"", ""},
	} {
		if got := httpURL(tt.in); got != tt.want {
			t.Errorf("httpURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
