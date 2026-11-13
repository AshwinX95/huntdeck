package syncer

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// syncInvites records the platform's pending invitations. Not reading
// them only costs this poll's update, so it's logged, not a failure.
func (s *Syncer) syncInvites(ctx context.Context, log *slog.Logger, il platform.InviteLister, platformName string) {
	invites, err := il.ListInvites(ctx)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, platform.ErrNoWebLogin) {
			log.Info("pending invitations not read", "err", err)
		}
		return
	}
	pending, accepted, err := s.store.SetInvites(ctx, platformName, invites, s.store.Now())
	if err != nil {
		log.Warn("store pending invitations", "err", err)
		return
	}
	if accepted > 0 || pending != len(invites) {
		log.Info("pending invitations", "pending", pending, "listed", len(invites), "accepted_since_last", accepted)
	}
}
