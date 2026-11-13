package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// HuntDeck has no login: it is a personal tool meant to be reached only from
// your own machine or network. Two browser-borne attacks still matter for a
// login-free local dashboard, and are blocked here and in NewHandler:
//
//   - Cross-site requests (CSRF): another site making your browser POST to
//     HuntDeck. http.CrossOriginProtection rejects those.
//   - DNS rebinding: a site whose hostname re-resolves to 127.0.0.1, letting
//     its scripts read HuntDeck as "same origin". hostGuard only accepts
//     requests addressed to an IP, localhost, or a configured hostname.

// hostGuard rejects requests whose Host header isn't an IP address,
// localhost, or one of allowed.
func hostGuard(allowed []string, next http.Handler) http.Handler {
	ok := map[string]bool{}
	for _, h := range allowed {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			ok[h] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || hostAllowed(r.Host, ok) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte("HuntDeck doesn't recognise the hostname in this request.\n\n" +
			"If you reach HuntDeck through a hostname (for example a tailnet or LAN name),\n" +
			"add it to HUNTDECK_ALLOWED_HOSTS and restart. Requests by IP address and\n" +
			"localhost always work.\n"))
	})
}

func hostAllowed(hostport string, allowed map[string]bool) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true // rebinding needs a hostname; IP literals are safe
	}
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || allowed[host]
}
