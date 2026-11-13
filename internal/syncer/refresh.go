package syncer

import (
	"context"
	"errors"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// ErrCannotAccept means the platform can't accept invitations for the user
// (it doesn't implement platform.InviteAccepter, or has no login for it).
var ErrCannotAccept = errors.New("this platform's invitations can't be accepted from HuntDeck")

// ErrNoInvite means the program has no pending invitation to accept.
var ErrNoInvite = errors.New("no pending invitation")

// CanAcceptInvites reports whether p's invitations can be accepted from
// HuntDeck.
func CanAcceptInvites(p platform.Platform) bool {
	_, ok := p.(platform.InviteAccepter)
	_, lists := p.(platform.InviteLister)
	return ok && lists
}

// AcceptInvite accepts the invitation to p's program handle on the
// platform and records it as joined. Refresh the program after it
// (RefreshPrograms): accepting can show more of it.
func (s *Syncer) AcceptInvite(ctx context.Context, p platform.Platform, handle string) error {
	ia, ok := p.(platform.InviteAccepter)
	if !ok || !CanAcceptInvites(p) {
		return ErrCannotAccept
	}
	d, err := s.store.Program(ctx, p.Name(), handle)
	if err != nil {
		return err
	}
	if !d.InvitePending {
		return fmt.Errorf("%s: %s: %w", p.Name(), handle, ErrNoInvite)
	}
	if err := ia.AcceptInvite(ctx, handle); err != nil {
		return err
	}
	s.log.Info("invitation accepted", "platform", p.Name(), "program", handle)
	return s.store.InviteAccepted(context.WithoutCancel(ctx), p.Name(), handle, s.store.Now())
}

// RefreshPrograms brings p's named programs up to date now: a full poll of
// just them (their listing entry, and their scope downloaded again), then
// their stats, from the browser sync's pages where it's on. The poll is a
// run with trigger store.TriggerProgram, which doesn't count as the
// platform's scheduled full poll and runs beside one rather than waiting
// for it. It returns store.ErrRunInProgress while another refresh of p
// runs.
func (s *Syncer) RefreshPrograms(ctx context.Context, p platform.Platform, handles []string) (store.Run, error) {
	run, err := s.Poll(ctx, p, Options{Mode: ModeFull, Trigger: store.TriggerProgram, Handles: handles, RefetchScope: true})
	if err != nil {
		return run, err
	}
	if _, ok := p.(platform.StatsFetcher); ok && run.Status != store.RunFailed {
		if _, failed, err := s.RefreshStats(ctx, p, handles); err != nil || failed > 0 {
			s.log.Warn("program stats not refreshed", "platform", p.Name(), "programs", handles, "failed", failed, "err", err)
		} else if err := huntscore.Refresh(ctx, s.store, s.store.Now()); err != nil {
			s.log.Warn("hunt scores not updated", "err", err)
		}
	}
	return run, nil
}
