package syncer

import (
	"context"
	"testing"
	"time"
)

func TestRunProgress(t *testing.T) {
	e := newEnv(t)
	stop := e.sync.trackRun(context.Background(), 7, e.fake)
	e.sync.step(7, "Program stats", 10)
	e.clock = e.clock.Add(time.Minute)
	e.sync.advance(7, 4)
	e.sync.step(0, "ignored", 1) // run 0 isn't tracked
	p, ok := e.sync.RunProgress(7)
	if !ok || p.Step != "Program stats" || p.Done != 4 || p.Total != 10 || !p.LastActivity.Equal(e.clock) || p.StepStarted.Equal(e.clock) {
		t.Errorf("progress = %+v, %v", p, ok)
	}
	stop()
	if _, ok := e.sync.RunProgress(7); ok {
		t.Error("a finished run still has progress")
	}

	// A poll is tracked while it runs and forgotten after.
	run := e.poll(ModeFull, 0)
	if _, ok := e.sync.RunProgress(run.ID); ok {
		t.Error("a finished poll still has progress")
	}
}
