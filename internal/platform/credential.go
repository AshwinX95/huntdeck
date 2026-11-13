package platform

import (
	"sync"
	"time"
)

// CredentialReporter is implemented by platforms that sign in, to say how
// each credential did when it was last used (Settings, "Sign-in").
type CredentialReporter interface {
	Credentials() []Credential
}

// Credential is one way a platform client signs in, and how that went.
// Kept in memory: after a restart it's unknown until the next use.
type Credential struct {
	Name     string     // "API token", "Login", "Session cookie", "Token"
	LastOK   *time.Time // last accepted
	LastFail *time.Time // last refused
	// Failure says why it was last refused, safe to show (no secrets);
	// "" once it has been accepted since.
	Failure string
	// Stuck says the platform refused it for good: no retries until it's
	// fixed in .env and HuntDeck restarted.
	Stuck bool
	// SignedIn is when a login last started a session (or renewed its
	// token), and Expires when that session or token runs out, where the
	// platform says; nil for credentials without a session (API tokens).
	SignedIn, Expires *time.Time
}

// CredentialState tracks one credential for a client; safe for concurrent
// use. The zero value is ready.
type CredentialState struct {
	mu sync.Mutex
	c  Credential
}

// Accepted records that the platform accepted the credential.
func (s *CredentialState) Accepted() {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.LastOK, s.c.Failure, s.c.Stuck = &now, "", false
}

// SignedIn records that a login started a session, which expires at
// expires (nil if not known). It counts as accepted.
func (s *CredentialState) SignedIn(expires *time.Time) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.LastOK, s.c.Failure, s.c.Stuck = &now, "", false
	s.c.SignedIn, s.c.Expires = &now, nil
	if expires != nil && !expires.IsZero() {
		e := expires.UTC()
		s.c.Expires = &e
	}
}

// ExpiresAt records when the current session runs out, as the platform
// says when asked about it.
func (s *CredentialState) ExpiresAt(t time.Time) {
	t = t.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.Expires = &t
}

// Refused records that the platform refused it, saying why; stuck if it
// won't be tried again.
func (s *CredentialState) Refused(why string, stuck bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.LastFail, s.c.Failure, s.c.Stuck = &now, why, stuck
}

// Report is the credential's state, named name.
func (s *CredentialState) Report(name string) Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.c
	c.Name = name
	return c
}
