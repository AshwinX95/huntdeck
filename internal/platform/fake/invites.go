package fake

import (
	"context"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// SetInvites sets what ListInvites returns, or err instead; see Inviting.
func (f *Platform) SetInvites(err error, in ...platform.Invite) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invites, f.invitesErr = in, err
}

// Inviting is a fake platform that also lists pending invitations, as
// Intigriti does.
type Inviting struct{ *Platform }

// ListInvites implements platform.InviteLister.
func (i Inviting) ListInvites(context.Context) ([]platform.Invite, error) {
	f := i.Platform
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.invitesErr != nil {
		return nil, f.invitesErr
	}
	return append([]platform.Invite(nil), f.invites...), nil
}

// AcceptInvite implements platform.InviteAccepter: the invitation leaves
// the list ListInvites returns. An unlisted one is an error, as on the
// platform.
func (i Inviting) AcceptInvite(_ context.Context, handle string) error {
	f := i.Platform
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	for n, in := range f.invites {
		if in.Handle == handle {
			f.invites = append(f.invites[:n:n], f.invites[n+1:]...)
			f.acceptedInvites = append(f.acceptedInvites, handle)
			return nil
		}
	}
	return fmt.Errorf("fake: no invitation to %s", handle)
}

// AcceptedInvites are the invitations AcceptInvite accepted.
func (f *Platform) AcceptedInvites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.acceptedInvites...)
}
