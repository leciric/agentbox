package daemon

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A disk at its floor refuses new work with 507 and a reason, pauses the agent
// writing the most and won't let it be resumed by hand, and once there's room
// again — here because the floor was lowered — resumes it, all with fake disk
// numbers.
func TestDiskGuardRefusesPausesAndResumes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)

	const gb = int64(1) << 30
	var free, written atomic.Int64
	var paused atomic.Bool
	var mu sync.Mutex
	var calls []string
	w := d.srv.disk
	w.measure = func(context.Context) []agent.DiskSpace {
		return []agent.DiskSpace{{Label: "Storage pool", Free: free.Load(), Total: 100 * gb}}
	}
	w.writers = func(context.Context) ([]agent.DiskWriter, error) {
		return []agent.DiskWriter{{Ref: a.Ref(), Instance: a.Instance, Written: written.Load(), Paused: paused.Load()}}, nil
	}
	record := func(what string, p bool) func(context.Context, string) error {
		return func(_ context.Context, ref string) error {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, what+" "+ref)
			paused.Store(p)
			return nil
		}
	}
	w.pause, w.resume = record("pause", true), record("resume", false)
	called := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}

	free.Store(50 * gb)
	d.srv.checkDisk(ctx)
	if g, err := d.client.DiskGuard(ctx); err != nil || g.Level != api.DiskOK || len(g.Disks) != 1 || g.Disks[0].Floor != 10*gb {
		t.Fatalf("with room: %+v, %v", g, err)
	}

	// At the floor: the first check only sees what the agent has written so
	// far, the next one what it wrote since.
	free.Store(9 * gb)
	d.srv.checkDisk(ctx)
	written.Store(200 << 20)
	d.srv.checkDisk(ctx)
	if got := called(); !slices.Equal(got, []string{"pause " + a.Ref()}) {
		t.Fatalf("calls = %q, want the agent paused", got)
	}
	g, err := d.client.DiskGuard(ctx)
	if err != nil || g.Level != api.DiskFull || !slices.Equal(g.Paused, []string{a.Ref()}) || !strings.Contains(g.Message, "9.0 GiB free") {
		t.Fatalf("at the floor: %+v, %v", g, err)
	}
	if v, _ := d.srv.store.Setting(ctx, state.SettingDiskGuardPaused); v != `["hello-stack/agent-01"]` {
		t.Errorf("paused list kept for a restart: %q", v)
	}

	_, err = d.client.BuildImage(ctx, api.BuildImageRequest{})
	var se *api.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusInsufficientStorage || !strings.Contains(se.Message, "not building the base image") {
		t.Errorf("building the image at the floor: %v", err)
	}
	if _, err := d.client.AgentAction(ctx, a.Ref(), "resume"); err == nil || !strings.Contains(err.Error(), "resumes it by itself") {
		t.Errorf("resuming the paused agent by hand: %v", err)
	}

	// A floor too low to react in is refused; lowering it to 2 GiB gives room.
	tooLow := int64(1) * gb
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{DiskFloorMin: &tooLow}); err == nil {
		t.Error("a 1 GiB floor was taken")
	}
	low, none := 2*gb, 0.0
	settings, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{DiskFloorMin: &low, DiskFloorPercent: &none})
	if err != nil || settings.DiskFloorMin != low || settings.DiskFloorPercent != 0 {
		t.Fatalf("UpdateSettings = %+v, %v", settings, err)
	}
	d.srv.checkDisk(ctx)
	if got := called(); !slices.Equal(got, []string{"pause " + a.Ref(), "resume " + a.Ref()}) {
		t.Errorf("calls = %q, want it resumed", got)
	}
	if g, _ := d.client.DiskGuard(ctx); g.Level != api.DiskOK || len(g.Paused) != 0 {
		t.Errorf("after lowering the floor: %+v", g)
	}

	// Back to the default.
	zero, negative := int64(0), -1.0
	if settings, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{DiskFloorMin: &zero, DiskFloorPercent: &negative}); err != nil ||
		settings.DiskFloorMin != agent.DefaultDiskFloorMin || settings.DiskFloorPercent != agent.DefaultDiskFloorPercent {
		t.Errorf("back to the default: %+v, %v", settings, err)
	}
}

// Queued agents wait while a disk is at its floor.
func TestDiskGuardHoldsTheQueue(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	d.srv.disk.measure = func(context.Context) []agent.DiskSpace {
		return []agent.DiskSpace{{Label: "Storage pool", Free: 1 << 30, Total: 100 << 30}}
	}
	d.srv.disk.writers = func(context.Context) ([]agent.DiskWriter, error) { return nil, nil }
	d.srv.checkDisk(ctx)
	if err := d.srv.diskRefusal("creating an agent"); err == nil || !isDiskFull(err) {
		t.Fatalf("diskRefusal = %v", err)
	}
	started := false
	d.srv.queueStart = func(context.Context, state.QueuedAgent) error { started = true; return nil }
	d.srv.admitQueued(ctx)
	if started {
		t.Error("a queued agent started at the floor")
	}
}
