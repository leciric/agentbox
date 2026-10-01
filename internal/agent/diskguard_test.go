package agent

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDiskFloorIsTheLargerOfMinAndPercentCappedOnSmallDisks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		floor DiskFloor
		total int64
		want  int64
	}{
		{"a 100 GiB pool keeps 10 GiB", DefaultDiskFloor, 100 * gib, 10 * gib},
		{"a 1 TiB disk keeps 5%", DefaultDiskFloor, 1024 * gib, 1024 * gib / 20},
		{"the VM's 20 GiB system disk keeps a quarter", DefaultDiskFloor, 20 * gib, 5 * gib},
		{"a chosen floor", DiskFloor{Min: 20 * gib, Percent: 0}, 500 * gib, 20 * gib},
		{"an unknown size keeps Min", DefaultDiskFloor, 0, 10 * gib},
	} {
		if got := tc.floor.For(tc.total); got != tc.want {
			t.Errorf("%s: For(%d) = %d, want %d", tc.name, tc.total, got, tc.want)
		}
	}
}

func TestDiskFloorValidate(t *testing.T) {
	t.Parallel()
	if err := DefaultDiskFloor.Validate(); err != nil {
		t.Errorf("the default floor: %v", err)
	}
	for _, bad := range []DiskFloor{{Min: gib, Percent: 5}, {Min: 10 * gib, Percent: -1}, {Min: 10 * gib, Percent: 60}} {
		if bad.Validate() == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
}

// fakePool is a 100 GiB storage pool with free GiB free: its floor is 10 GiB, and
// the guard warns under 15.
func fakePool(free float64) DiskSpace {
	return DiskSpace{Label: "Storage pool", Free: int64(free * float64(gib)), Total: 100 * gib}
}

func TestDiskGuardWarnsNearTheFloorAndRefusesAtIt(t *testing.T) {
	t.Parallel()
	g := NewDiskGuard(nil)
	now := time.Now()
	home := DiskSpace{Label: "Worktrees", Path: "/home/u", Free: 400 * gib, Total: 1000 * gib}

	step := g.Step(now, DefaultDiskFloor, []DiskSpace{fakePool(50), home}, nil)
	if step.Status.Level != DiskOK || step.Changed {
		t.Fatalf("plenty free: level %q, changed %v", step.Status.Level, step.Changed)
	}
	if err := step.Status.Refusal("creating an agent"); err != nil {
		t.Errorf("refused with room: %v", err)
	}

	step = g.Step(now.Add(time.Second), DefaultDiskFloor, []DiskSpace{fakePool(14), home}, nil)
	if step.Status.Level != DiskLow || !step.Changed {
		t.Fatalf("14 GiB of 100: level %q, changed %v, want low", step.Status.Level, step.Changed)
	}
	if step.Status.Refusal("creating an agent") != nil {
		t.Error("nearing the floor already refuses")
	}

	step = g.Step(now.Add(2*time.Second), DefaultDiskFloor, []DiskSpace{fakePool(9.5), home}, nil)
	if step.Status.Level != DiskFull || !step.Changed {
		t.Fatalf("9.5 GiB of 100: level %q, want full", step.Status.Level)
	}
	if !step.Status.Since.Equal(now.Add(2 * time.Second)) {
		t.Errorf("Since = %v, want when it got full", step.Status.Since)
	}
	err := step.Status.Refusal("creating an agent")
	if err == nil || !strings.Contains(err.Error(), "Storage pool has 9.5 GiB free") || !strings.Contains(err.Error(), "under the 10.0 GiB") {
		t.Errorf("Refusal = %v", err)
	}
	// The worst disk is the one named, whichever order they came in.
	worst, _ := step.Status.Worst()
	if worst.Label != "Storage pool" {
		t.Errorf("Worst = %q", worst.Label)
	}
}

func TestDiskGuardLeavesOutDisksItCouldNotMeasure(t *testing.T) {
	t.Parallel()
	step := NewDiskGuard(nil).Step(time.Now(), DefaultDiskFloor, nil, nil)
	if step.Status.Level != DiskOK || len(step.Status.Disks) != 0 {
		t.Errorf("nothing measured: %+v", step.Status)
	}
}

func TestDiskGuardPausesTheTopWriterOneACheckAndResumesWithRoom(t *testing.T) {
	t.Parallel()
	g := NewDiskGuard(nil)
	now := time.Now()
	writers := func(a, b, c int64, paused ...string) []DiskWriter {
		ws := []DiskWriter{
			{Ref: "p/agent-01", Instance: "ab-p-agent-01", Written: a},
			{Ref: "p/agent-02", Instance: "ab-p-agent-02", Written: b},
			{Ref: "p/agent-03", Instance: "ab-p-agent-03", Written: c},
		}
		for i := range ws {
			ws[i].Paused = slices.Contains(paused, ws[i].Ref)
		}
		return ws
	}

	// The first sight of an agent counts nothing: it may have written its
	// gigabytes long before the disk got near full.
	step := g.Step(now, DefaultDiskFloor, []DiskSpace{fakePool(8)}, writers(100*gib, 0, 0))
	if step.Pause != nil {
		t.Fatalf("paused %s on its first sight", step.Pause.Ref)
	}

	// agent-02 wrote the most since: it goes first.
	step = g.Step(now.Add(2*time.Second), DefaultDiskFloor, []DiskSpace{fakePool(7)}, writers(100*gib+50<<20, 2*gib, 10<<20))
	if step.Pause == nil || step.Pause.Ref != "p/agent-02" {
		t.Fatalf("Pause = %+v, want agent-02", step.Pause)
	}
	if !slices.Equal(step.Status.Paused, []string{"p/agent-02"}) || !step.Changed {
		t.Errorf("Paused = %q, changed %v", step.Status.Paused, step.Changed)
	}

	// Still full, still falling: the next writer, never a paused one.
	step = g.Step(now.Add(4*time.Second), DefaultDiskFloor, []DiskSpace{fakePool(6.9)}, writers(100*gib+150<<20, 2*gib, 20<<20, "p/agent-02"))
	if step.Pause == nil || step.Pause.Ref != "p/agent-01" {
		t.Fatalf("Pause = %+v, want agent-01", step.Pause)
	}

	// An agent that wrote under a MiB since isn't the one filling it.
	step = g.Step(now.Add(6*time.Second), DefaultDiskFloor, []DiskSpace{fakePool(6.9)}, writers(100*gib+150<<20, 2*gib, 20<<20+1000, "p/agent-01", "p/agent-02"))
	if step.Pause != nil {
		t.Errorf("paused %s, which barely wrote", step.Pause.Ref)
	}

	// Between the floor and the warning line, nothing moves either way.
	step = g.Step(now.Add(8*time.Second), DefaultDiskFloor, []DiskSpace{fakePool(12)}, writers(100*gib+150<<20, 2*gib, 30<<30, "p/agent-01", "p/agent-02"))
	if step.Pause != nil || len(step.Resume) != 0 {
		t.Errorf("nearing the floor: pause %+v, resume %q", step.Pause, step.Resume)
	}

	// Back above it, both are resumed.
	step = g.Step(now.Add(10*time.Second), DefaultDiskFloor, []DiskSpace{fakePool(20)}, writers(0, 0, 0, "p/agent-01", "p/agent-02"))
	if !slices.Equal(step.Resume, []string{"p/agent-01", "p/agent-02"}) {
		t.Errorf("Resume = %q", step.Resume)
	}
	if len(step.Status.Paused) != 0 || step.Status.Level != DiskOK {
		t.Errorf("after resuming: %+v", step.Status)
	}
}

func TestDiskGuardForgetsAgentsSomeoneElseResumedOrStopped(t *testing.T) {
	t.Parallel()
	g := NewDiskGuard([]string{"p/agent-01", "p/agent-02"})
	if !g.PausedByGuard("p/agent-01") {
		t.Fatal("the paused list a restart brought back was lost")
	}
	// agent-01 was resumed by hand, agent-02 stopped (gone from the writers).
	step := g.Step(time.Now(), DefaultDiskFloor, []DiskSpace{fakePool(5)}, []DiskWriter{{Ref: "p/agent-01", Instance: "i1"}})
	if len(step.Status.Paused) != 0 {
		t.Errorf("Paused = %q, want none", step.Status.Paused)
	}
	step = g.Step(time.Now(), DefaultDiskFloor, []DiskSpace{fakePool(50)}, []DiskWriter{{Ref: "p/agent-01", Instance: "i1"}})
	if len(step.Resume) != 0 {
		t.Errorf("resumed %q, which it no longer holds", step.Resume)
	}
}

func TestDiskGuardResumesWhatARestartBroughtBack(t *testing.T) {
	t.Parallel()
	g := NewDiskGuard([]string{"p/agent-01"})
	step := g.Step(time.Now(), DefaultDiskFloor, []DiskSpace{fakePool(50)}, []DiskWriter{{Ref: "p/agent-01", Instance: "i1", Paused: true}})
	if !slices.Equal(step.Resume, []string{"p/agent-01"}) {
		t.Errorf("Resume = %q", step.Resume)
	}
}

func TestDiskGuardTakesACounterThatWentBackAsNothing(t *testing.T) {
	t.Parallel()
	g := NewDiskGuard(nil)
	now := time.Now()
	g.Step(now, DefaultDiskFloor, []DiskSpace{fakePool(5)}, []DiskWriter{{Ref: "p/a", Instance: "i", Written: 10 * gib}})
	// The machine restarted: its counter starts again from nothing.
	step := g.Step(now, DefaultDiskFloor, []DiskSpace{fakePool(5)}, []DiskWriter{{Ref: "p/a", Instance: "i", Written: 5 << 20}})
	if step.Pause != nil {
		t.Error("a restarted counter was taken for a writer")
	}
	step = g.Step(now, DefaultDiskFloor, []DiskSpace{fakePool(5)}, []DiskWriter{{Ref: "p/a", Instance: "i", Written: 50 << 20}})
	if step.Pause == nil {
		t.Error("what it wrote after the restart didn't count")
	}
}

func TestDiskGuardStillRefusesWithoutTheAgents(t *testing.T) {
	t.Parallel()
	g := NewDiskGuard([]string{"p/agent-01"})
	step := g.Step(time.Now(), DefaultDiskFloor, []DiskSpace{fakePool(5)}, nil)
	if step.Status.Level != DiskFull || step.Pause != nil || !slices.Equal(step.Status.Paused, []string{"p/agent-01"}) {
		t.Errorf("agents unknown at the floor: %+v", step)
	}
	step = g.Step(time.Now(), DefaultDiskFloor, []DiskSpace{fakePool(50)}, nil)
	if len(step.Resume) != 0 || !g.PausedByGuard("p/agent-01") {
		t.Errorf("resumed %q without knowing it's still paused", step.Resume)
	}
}
