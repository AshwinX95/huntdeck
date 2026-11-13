package store

import (
	"context"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// InviteAccepted records that the user accepted the invitation to
// platformName's program handle at now.
func (s *Store) InviteAccepted(ctx context.Context, platformName, handle string, now time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `UPDATE programs SET invite_pending = 0, joined_at = COALESCE(joined_at, ?)
			WHERE platform = ? AND handle = ?`, ts(now), platformName, handle)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetInvites records platformName's pending invitations, the whole list as
// the platform gave it: the programs in it are marked pending (with when
// they were invited), and ones that were pending and aren't any more are
// taken as accepted at now, unless joined_at already says when. Invitations
// for programs not stored yet are skipped. It returns how many programs
// are pending and how many were accepted since the last call.
func (s *Store) SetInvites(ctx context.Context, platformName string, invites []platform.Invite, now time.Time) (pending, accepted int, err error) {
	handles := make([]any, 0, len(invites))
	for _, in := range invites {
		handles = append(handles, in.Handle)
	}
	notListed := "1"
	if len(handles) > 0 {
		notListed = "handle NOT IN (" + placeholders(len(handles)) + ")"
	}
	err = s.Write(ctx, func(tx *Tx) error {
		pending, accepted = 0, 0 // Write may retry the function
		//nolint:gosec // placeholders only
		res, err := tx.tx.ExecContext(ctx, `UPDATE programs SET invite_pending = 0, joined_at = COALESCE(joined_at, ?)
			WHERE platform = ? AND invite_pending = 1 AND `+notListed, append([]any{ts(now), platformName}, handles...)...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		accepted = int(n)
		for _, in := range invites {
			var at *time.Time
			if !in.InvitedAt.IsZero() {
				at = &in.InvitedAt
			}
			res, err := tx.tx.ExecContext(ctx, `UPDATE programs SET invite_pending = 1, invited_at = COALESCE(?, invited_at)
				WHERE platform = ? AND handle = ?`, tsPtr(at), platformName, in.Handle)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			pending += int(n)
		}
		return nil
	})
	return pending, accepted, err
}
