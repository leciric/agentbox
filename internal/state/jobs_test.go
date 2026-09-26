package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestHasRunningJob is what List (internal/agent) leans on to tell an agent
// still being made, whose create job is still running, from one left behind
// by a create that failed or was interrupted, with nothing running for it
// anymore.
func TestHasRunningJob(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))

	if has, err := st.HasRunningJob(ctx, "create", "pawly"); err != nil || has {
		t.Fatalf("HasRunningJob() with no jobs at all = %v, %v, want false, nil", has, err)
	}

	if err := st.AddJob(ctx, state.Job{ID: "j1", Kind: "create", Target: "pawly", Status: "running", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if has, err := st.HasRunningJob(ctx, "create", "pawly"); err != nil || !has {
		t.Fatalf("HasRunningJob() with a running create job = %v, %v, want true, nil", has, err)
	}
	// A different project, or a different kind, doesn't count.
	if has, err := st.HasRunningJob(ctx, "create", "other"); err != nil || has {
		t.Fatalf("HasRunningJob() for another project = %v, %v, want false, nil", has, err)
	}
	if has, err := st.HasRunningJob(ctx, "fork", "pawly"); err != nil || has {
		t.Fatalf("HasRunningJob() for another kind = %v, %v, want false, nil", has, err)
	}

	if err := st.FinishJob(ctx, state.Job{ID: "j1", Status: "succeeded", FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if has, err := st.HasRunningJob(ctx, "create", "pawly"); err != nil || has {
		t.Fatalf("HasRunningJob() once the job finished = %v, %v, want false, nil", has, err)
	}
}
