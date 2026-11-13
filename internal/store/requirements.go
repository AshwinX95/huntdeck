package store

import (
	"context"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// SetRequirements records what a program requires of hackers.
func (t *Tx) SetRequirements(ctx context.Context, programID int64, r platform.Requirements) error {
	_, err := t.tx.ExecContext(ctx, `
		UPDATE programs SET req_known = 1, req_min_signal = ?, req_id_verified = ?, req_cleared = ?, req_application = ?,
			req_eea = ?, req_two_factor = ?, req_terms = ?
		WHERE id = ?`, r.MinSignal, b2i(r.IDVerified), b2i(r.Cleared), b2i(r.Application), b2i(r.EEA), b2i(r.TwoFactor), b2i(r.Terms), programID)
	return err
}

// ProgramRequirements is what a program requires of hackers, as stored.
type ProgramRequirements struct {
	Known bool // false until a listing or stats fetch has said
	platform.Requirements
}

// None reports whether the program is known to accept anyone's reports.
// Two-factor auth and terms don't count: anyone can turn on or accept them.
func (r ProgramRequirements) None() bool {
	return r.Known && r.MinSignal == nil && !r.IDVerified && !r.Cleared && !r.Application && !r.EEA
}

const requirementColumns = `p.req_known, p.req_min_signal, p.req_id_verified, p.req_cleared, p.req_application, p.req_eea, p.req_two_factor, p.req_terms`

func (r *ProgramRequirements) scanTargets() []any {
	return []any{&r.Known, &r.MinSignal, &r.IDVerified, &r.Cleared, &r.Application, &r.EEA, &r.TwoFactor, &r.Terms}
}
