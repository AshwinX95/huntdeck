package web

import (
	"context"
	"net/mail"
	"regexp"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Which program mail sent to an address HuntDeck didn't make is for: a
// guess, shown as one, from the first of
//
//  1. the address starting with a program's name or handle ("acme-…",
//     "acme.signup@…"): the user named it so;
//  2. the sender's root domain in a program's scope;
//  3. the sender's root domain named as a program is (acme.example for
//     "Acme", whose scope may list only acme-app.example).
//
// Names are compared as MailSlug writes them (letters and digits); words
// any address or sender might use (test, news, shop…) never count as one.

// GuessMore is how many programs the guess names beyond its first.
func (m mailRow) GuessMore() int { return max(len(m.Guess)-1, 0) }

// notPrograms are senders' root domains that never say which program mail
// is for: the platforms' own notifications, and free mail.
var notPrograms = map[string]bool{
	"hackerone.com": true, "wearehackerone.com": true, "hackerone.net": true, "bugcrowd.com": true, "bugcrowdninja.com": true,
	"intigriti.com": true, "intigriti.io": true, "intigriti.me": true, "yeswehack.com": true, "yeswehack.ninja": true,
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true, "yahoo.com": true,
	"icloud.com": true, "me.com": true, "proton.me": true, "protonmail.com": true, "gmx.com": true, "aol.com": true,
}

// notNames are words too common in addresses and senders' domains to name
// a program by, even when one is called that.
var notNames = map[string]bool{
	"test": true, "tests": true, "testing": true, "mail": true, "email": true, "user": true, "users": true, "admin": true,
	"info": true, "news": true, "shop": true, "team": true, "hello": true, "hunt": true, "hunter": true, "signup": true,
	"account": true, "accounts": true, "login": true, "support": true, "noreply": true, "contact": true, "demo": true,
	"prod": true, "staging": true, "security": true, "example": true, "victim": true, "attacker": true, "bounce": true,
}

// minNameLen is the shortest name a guess goes by.
const minNameLen = 4

// nameWord splits an address's local part into words.
var nameWord = regexp.MustCompile(`[a-z0-9]+`)

// addressNames are the names an address could start with: its first word,
// and its first two together ("acme-corp-1" for "Acme Corp").
func addressNames(addr string) []string {
	local, _, _ := strings.Cut(strings.ToLower(addr), "@")
	words := nameWord.FindAllString(local, 2)
	var out []string
	if len(words) > 0 {
		out = append(out, words[0])
	}
	if len(words) > 1 && strings.Trim(words[1], "0123456789") != "" {
		out = append(out, words[0]+words[1])
	}
	return out
}

// isName says a word can name a program.
func isName(w string) bool { return len(w) >= minNameLen && !notNames[w] }

// senderApex is the root domain of the address the mail says it's from
// (the From header, else the envelope sender), "" when there's none.
func senderApex(e huntmail.Email) string {
	addr := e.From
	if a, err := mail.ParseAddress(e.HeaderFrom); err == nil {
		addr = a.Address
	}
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(addr)), "@")
	if !ok || domain == "" {
		return ""
	}
	return scope.RootDomain(strings.TrimSuffix(domain, "."))
}

// guessPrograms fills in, for rows whose address wasn't made for a
// program, the programs they're likeliest for, and why.
func (s *server) guessPrograms(ctx context.Context, rows []mailRow) {
	var todo []int
	for i := range rows {
		if rows[i].Program == nil {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return
	}
	named, err := s.Store.ProgramKeys(ctx)
	if err != nil {
		s.Log.Warn("email: programs by name", "err", err)
		return
	}
	// 1. The address names it.
	apexOf := map[int]string{}
	var apexes []string
	for _, i := range todo {
		for _, w := range addressNames(rows[i].To) {
			if ps := named[w]; isName(w) && len(ps) > 0 {
				rows[i].Guess, rows[i].GuessWhy = ps, "the address starts with "+w+", as this program is named"
			}
		}
		if rows[i].Guess != nil {
			continue
		}
		if a := senderApex(rows[i].Email); a != "" && !notPrograms[a] {
			apexOf[i] = a
			if !slices.Contains(apexes, a) {
				apexes = append(apexes, a)
			}
		}
	}
	// 2. The sender's domain is in scope.
	inScope, err := s.Store.ProgramsUnder(ctx, apexes)
	if err != nil {
		s.Log.Warn("email: programs by sender", "err", err)
		return
	}
	for i, a := range apexOf {
		if ps := inScope[a]; len(ps) > 0 {
			rows[i].Guess, rows[i].GuessWhy = ps, "the sender's domain, "+a+", is in this program's scope"
			continue
		}
		// 3. The sender's domain is named as the program is.
		label, _, _ := strings.Cut(a, ".")
		if w := store.MailSlug(label); isName(w) && len(named[w]) > 0 {
			rows[i].Guess, rows[i].GuessWhy = named[w], "the sender's domain, "+a+", is named as this program is"
		}
	}
}
