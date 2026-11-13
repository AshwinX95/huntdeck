package syncer

import (
	"context"
	"errors"

	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/store"
)

// SaveOwn stores the programs added by hand: the whole listing, with an
// edit applied, polled like a platform's (see internal/platform/
// independent). A program left out of programs is removed: the user took
// it out, so the mass-removal guard doesn't apply. It returns
// store.ErrRunInProgress while another save runs.
func (s *Syncer) SaveOwn(ctx context.Context, programs []independent.Program) error {
	run, err := s.Poll(ctx, independent.New(programs), Options{Mode: ModeFull, Trigger: store.TriggerEdit,
		ForceRemovals: true, RefetchScope: true})
	if err != nil {
		return err
	}
	if run.Status != store.RunOK {
		return errors.New(run.Error)
	}
	return nil
}
