package web

import (
	"net/http"
	"net/url"
	"strings"
)

// Feedback goes by email to HUNTDECK_FEEDBACK_EMAIL. Pages link to
// /feedback, which offers the address to copy and a new message in Gmail,
// Outlook.com or the visitor's mail app, each filled in with a subject and
// the page they came from: a bare mailto: link does nothing for someone
// whose mail is only in the browser. HuntDeck sends nothing itself.

// feedbackPage is the link to the feedback page from page (a path and
// query), or "" without an address.
func feedbackPage(address, page string) string {
	if address == "" {
		return ""
	}
	if strings.HasPrefix(page, "/feedback") {
		return page
	}
	return "/feedback?from=" + url.QueryEscape(page)
}

// feedbackPages are the first path segments of HuntDeck's pages, the
// only ones a feedback link may say it came from.
var feedbackPages = map[string]bool{"": true, "programs": true, "invites": true, "stats": true, "targets": true, "changes": true,
	"disclosed": true, "methodology": true, "api": true, "settings": true, "match": true, "search": true}

// feedbackFrom is the page a feedback link came from, if it's one of
// HuntDeck's, else "". The page shows it and puts it in the message, so
// it's held to a page's path and query, in URL characters only, short:
// a link can't make the page show, or the message say, words of its own.
func feedbackFrom(raw string) string {
	if len(raw) > 200 || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	for _, c := range raw {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/-._~%?=&+,:", c)
		if !ok {
			return ""
		}
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(raw, "/"), "/")
	first, _, _ = strings.Cut(first, "?")
	if !feedbackPages[first] {
		return ""
	}
	return raw
}

// feedbackData is the feedback page: the address and three ways to write.
type feedbackData struct {
	Address, Page          string
	Mailto, Gmail, Outlook string
}

// feedbackMessage is the subject and body a message starts with.
func feedbackMessage(page string, demo bool) (subject, body string) {
	subject = "HuntDeck feedback"
	if demo {
		subject = "HuntDeck Public feedback"
	}
	if page != "" {
		body = "Page: " + page + "\n\n"
	}
	return subject, body + "What happened, or what would you like to see?\n\n"
}

func newFeedbackData(address, page string, demo bool) feedbackData {
	subject, body := feedbackMessage(page, demo)
	enc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") } // mailto wants %20
	to := enc(address)
	return feedbackData{
		Address: address, Page: page,
		Mailto:  "mailto:" + address + "?subject=" + enc(subject) + "&body=" + enc(body),
		Gmail:   "https://mail.google.com/mail/?view=cm&fs=1&to=" + to + "&su=" + enc(subject) + "&body=" + enc(body),
		Outlook: "https://outlook.live.com/mail/0/deeplink/compose?to=" + to + "&subject=" + enc(subject) + "&body=" + enc(body),
	}
}

// feedback serves the feedback page; 404 without an address.
func (s *server) feedback(w http.ResponseWriter, r *http.Request) {
	if s.FeedbackEmail == "" {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
		return
	}
	d := newFeedbackData(s.FeedbackEmail, feedbackFrom(r.URL.Query().Get("from")), s.ReadOnly)
	s.render(w, r, http.StatusOK, "feedback", pageData{Title: "Feedback", Nav: "feedback", Data: d})
}
