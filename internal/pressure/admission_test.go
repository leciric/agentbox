package pressure

import (
	"testing"
	"time"
)

// Machines boot when asked, oldest first, one a BootStep, whatever the VM's
// memory: nothing waits for it (19 Nov 2026, the user's decision).
func TestAdmissionBootsWhenAskedOneABootStep(t *testing.T) {
	a := NewAdmission()
	now := t0
	a.Ask("p/new", t0.Add(time.Hour), now)
	a.Ask("p/old", t0, now)
	if !a.Waits("p/new") || a.Waiting()[0].Agent != "p/old" {
		t.Fatalf("waiting %v, want the older p/old first", a.Waiting())
	}
	if got := a.Step(now); len(got) != 1 || got[0] != "p/old" || a.Why() != WaitTurn {
		t.Fatalf("booted %v (%q), want p/old, p/new next", got, a.Why())
	}
	if got := a.Step(now.Add(2 * time.Second)); len(got) != 0 || a.Why() != WaitTurn {
		t.Fatalf("booted %v within BootStep (%q)", got, a.Why())
	}
	if got := a.Step(now.Add(DefaultBootStep)); len(got) != 1 || got[0] != "p/new" {
		t.Fatalf("booted %v a BootStep later, want p/new", got)
	}
	if len(a.Waiting()) != 0 || a.Why() != "" {
		t.Errorf("still waiting: %v (%q)", a.Waiting(), a.Why())
	}
	if !a.Booted("p/new") || !a.Ask("p/new", t0, now) {
		t.Error("a booted machine asking again waits")
	}
	a.Ask("p/later", t0, now)
	a.Cancel("p/later")
	if a.Waits("p/later") {
		t.Error("still in line after it gave up")
	}
}

// The readings of 19 Nov 2026, VM at its 8 GiB cap: 2 to 4 GiB of it free,
// every agent throttled at the shared limit with builds waiting, and the
// host with 8.2 of 30.5 GiB free, under its 7.6 GiB reserve, its own memory
// pressure 0: every boot waited, for twenty minutes and more. None of that
// is admission's to read any more: what holds a boot is the host under its
// hard floor (2 GiB, or 6% of it: 1.8 GiB of 30.5) now, and nothing else.
func TestAdmissionWaitsOnlyForTheHostsHardFloor(t *testing.T) {
	a := NewAdmission()
	a.Ask("p/a", t0, t0)
	a.HostShort = true
	for sec := 0; sec < 60; sec += 2 {
		if got := a.Step(t0.Add(time.Duration(sec) * time.Second)); len(got) != 0 || a.Why() != WaitHost {
			t.Fatalf("booted %v with the host under its floor (%q)", got, a.Why())
		}
	}
	if a.Holds(t0) != WaitHost {
		t.Errorf("Holds() = %q with the host under its floor", a.Holds(t0))
	}
	a.HostShort = false
	if got := a.Step(t0.Add(time.Minute)); len(got) != 1 {
		t.Fatalf("booted %v once the host was over its floor, want p/a", got)
	}
}

// What was last resumed has settled once the VM's memory doesn't stall, its
// disk isn't paged to, the computer isn't busy and, while asked, memory
// isn't falling fast.
func TestUnsettled(t *testing.T) {
	p := DefaultPolicy
	calm := Sample{PSI: PSI{Some: Stall{Avg10: 2}, Full: Stall{Avg10: 1}}}
	for _, c := range []struct {
		name   string
		s      Sample
		t      Trend
		steady bool
		want   bool
	}{
		{"calm", calm, Trend{}, true, false},
		{"some stall", Sample{PSI: PSI{Some: Stall{Avg10: p.Admit}}}, Trend{}, true, true},
		{"full stall", Sample{PSI: PSI{Full: Stall{Avg10: p.Calm}}}, Trend{}, true, true},
		{"disk swap", Sample{Mem: Mem{Total: 8 << 30, DiskSwapTotal: 4 << 30, DiskSwapFree: 4<<30 - DiskSwapTolerance}}, Trend{}, true, true},
		{"host busy", Sample{HostBusy: true}, Trend{}, true, true},
		{"falling", calm, Trend{Fall: SettleFall}, true, true},
		{"falling, steady no longer asked", calm, Trend{Fall: SettleFall}, false, false},
		{"falling slowly", calm, Trend{Fall: SettleFall / 2}, true, false},
	} {
		if got := p.Unsettled(c.s, c.t, c.steady); (got != "") != c.want {
			t.Errorf("%s: Unsettled() = %q, want unsettled %v", c.name, got, c.want)
		}
	}
}
