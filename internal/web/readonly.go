package web

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// A read-only install (Deps.ReadOnly, HUNTDECK_READ_ONLY) is for showing
// other people, such as a demo loaded from a seed. readOnly lets through
// only what reads: every request that could change something is refused,
// so a route added later is refused unless it only reads. The user's own
// pages (Settings, invites, notifications) are still shown, as a tour:
// NewHandler drops the account's credentials, browser and restart, so
// they show their layout with nothing in it, and every form on them is
// switched off (app.js; the server refuses them anyway). What can't be
// shown at all, the login browser's live display, isn't served.

// readOnlyHidden are path prefixes a read-only install doesn't serve.
var readOnlyHidden = []string{"/novnc", "/api/v1/invites", "/api/v2/invites", "/settings/restart"}

// readOnlyPOST are POST routes that only read (a batch of lookups).
var readOnlyPOST = map[string]bool{"/api/v1/match": true}

func (s *server) readOnly(next http.Handler) http.Handler {
	if !s.ReadOnly {
		return next
	}
	visitors := newVisitorLimiter(s.ClientIPHeader, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := visitors.allow(r); !ok {
			s.tooMany(w, r, wait)
			return
		}
		api := strings.HasPrefix(r.URL.Path, "/api/")
		for _, p := range readOnlyHidden {
			if r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/") {
				if api {
					writeJSONError(w, http.StatusNotFound, "not available in this read-only copy")
				} else {
					s.renderError(w, r, http.StatusNotFound, "This page isn't part of this read-only copy of HuntDeck.")
				}
				return
			}
		}
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
		case r.Method == http.MethodPost && readOnlyPOST[r.URL.Path]:
		case api:
			writeJSONError(w, http.StatusForbidden, "this copy of HuntDeck is read-only")
			return
		default:
			s.renderError(w, r, http.StatusForbidden, "This copy of HuntDeck is read-only: nothing can be changed here.")
			return
		}
		// Shown to anyone: no request runs on for long, so a costly one
		// can't hold the server (the store's queries honour it).
		ctx, cancel := context.WithTimeout(r.Context(), readOnlyDeadline)
		defer cancel()
		if !strings.HasPrefix(r.URL.Path, "/static/") && r.URL.Path != "/healthz" {
			w = &cacheWriter{ResponseWriter: w}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// readOnlyDeadline is the longest a read-only copy works on one request.
const readOnlyDeadline = 20 * time.Second

// demoTour is what a read-only copy says at the top of a page it shows
// empty, the user's own pages and private programs' stats (by the page's
// nav key and its visibility filter); "" for other pages.
func demoTour(nav, visibility string) string {
	switch nav {
	case "stats":
		if visibility == "private" {
			return "In your own copy, Private shows the stats of the private programs you're invited to, beside the public ones. " +
				"HuntDeck Public holds public programs only, so there's nothing here."
		}
	case "settings":
		return "This is how Settings looks in your own copy, empty and switched off: nothing here can be saved, and HuntDeck Public holds no credentials. " +
			"In yours, it's where you add your platform accounts, set how often each platform is polled, weight HuntScore and watch the login browser."
	case "invites":
		return "In your own copy, your private invites from all four platforms show up here, waiting or joined, with their scope and bounties. " +
			"HuntDeck Public holds public programs only, so there are none."
	case "notifications":
		return "In your own copy, this is where you send scope changes, new programs and invites to Discord, Telegram or a webhook. " +
			"HuntDeck Public sends nothing and keeps no channels."
	}
	return ""
}
