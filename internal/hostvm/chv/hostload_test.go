package chv

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/hostload"
)

// The user's 32 GB Linux PC on 18 Nov 2026: MemTotal about 30.4 GiB, so a
// 7.6 GiB reserve, 5.3 GiB available and no stall at all; the VM at its 12.8 GiB cap
// with 6.2 GiB available inside, 6 GiB of it page cache (3.5 GiB active), and
// a floor its builds' refaults had raised to 6 GiB.
func recordedHost() (*memSample, memState) {
	x := sample(6*GiB+600<<20, 12*GiB+800<<20, 6, 0)
	x.FileCache, x.InactiveFile = 6*GiB, 2*GiB+GiB/2
	x.Resident = 12*GiB + 600<<20
	x.HostTotal, x.HostAvailable, x.HostPressure = 30*GiB+384<<20, 5*GiB+300<<20, 0
	return x, memState{Requested: x.Total, floor: 6 * GiB}
}

// hostReading is what hostLoadLoop reports for x: the host's own reading,
// with what the VM can give back.
func hostReading(x *memSample) hostload.Reading {
	return hostload.Reading{
		OS: "linux", Memory: x.HostPressure, CPU: 1, IO: 1, CPUShare: -1,
		MemoryTotal: x.HostTotal, Available: x.HostAvailable, Reclaimable: x.reclaimable(),
	}
}

// What held every build and boot for half an hour holds nothing now: the VM
// can give back the 2.3 GiB the host is short of from its cache, and does.
func TestRecordedHostShortOfItsReserveHoldsNothing(t *testing.T) {
	x, st := recordedHost()
	if got := HostReserve(x.HostTotal); got != hostload.Reserve(x.HostTotal) || got != 7*GiB+608<<20 {
		t.Fatalf("reserve %s, hostload's %s, want 7.6 GiB for both", gib(got), gib(hostload.Reserve(x.HostTotal)))
	}
	// Its cache beyond the allowance (a quarter of 6.6 GiB in use).
	if got := x.reclaimable(); got < 4*GiB+300<<20 || got > 4*GiB+400<<20 {
		t.Errorf("reclaimable = %s, want about 4.35 GiB", gib(got))
	}
	if l := (&hostload.Judge{}).Judge(hostReading(x), time.Now()); l.Busy {
		t.Errorf("held: %+v", l)
	}
	// Before: the same reading, without what the VM could give back.
	before := hostReading(x)
	before.Reclaimable = 0
	if l := (&hostload.Judge{}).Judge(before, time.Now()); !l.Busy || hostload.Words(l) != "memory, 5.3 GiB available of the 7.6 GiB it keeps" {
		t.Errorf("without the VM's cache: %+v (%s)", l, hostload.Words(l))
	}

	// The memory loop gives it back: the floor yields by what the host is
	// short of, though the floor alone would have trimmed nothing.
	p := memPolicy{Min: 4 * GiB, Cap: x.Total}
	if n := x.FileCache - st.cacheFloor(*x, p); n > 0 {
		t.Fatalf("the floor alone leaves %s to trim; the recorded VM had none", gib(n))
	}
	now := time.Now()
	d := p.decide(&st, x, now)
	if d.Trim != memTrimMax || !d.HostShort || !strings.Contains(d.Reason, "5.3 GiB available of the 7.6 GiB it keeps") {
		t.Fatalf("first trim: %+v", d)
	}
	// 2 GiB of it back: the host is 0.3 GiB short, and the next trim takes
	// that, from what is left inactive, though the floor is still 6 GiB.
	x.FileCache, x.InactiveFile, x.Resident, x.HostAvailable = 4*GiB, GiB/2, x.Resident-2*GiB, x.HostAvailable+2*GiB
	if d := p.decide(&st, x, now.Add(memTick)); d.Trim != 0 {
		t.Fatalf("trimmed again within memTrimEvery: %+v", d)
	}
	d = p.decide(&st, x, now.Add(memTrimEvery))
	if short := HostReserve(x.HostTotal) - x.HostAvailable; short < 300<<20 || d.Trim != short {
		t.Fatalf("second trim: %s (%+v), want the 0.3 GiB it is short", gib(d.Trim), d)
	}
	// Back at its reserve: the cache left is the VM's, and nothing is held.
	x.FileCache, x.InactiveFile, x.Resident, x.HostAvailable = x.FileCache-d.Trim, x.InactiveFile-d.Trim, x.Resident-d.Trim, x.HostAvailable+d.Trim
	if d := p.decide(&st, x, now.Add(2*memTrimEvery)); d.Trim != 0 || d.Reclaim || d.Shrink {
		t.Errorf("with the host at its reserve: %+v", d)
	}
	if l := (&hostload.Judge{}).Judge(hostReading(x), now.Add(time.Minute)); l.Busy {
		t.Errorf("held at its reserve: %+v", l)
	}
}

// The gate still holds when the host would be short even with the VM's
// cache back, or stalls on memory whatever the VM could give.
func TestRecordedHostStillHeldWhenReallyShort(t *testing.T) {
	x, _ := recordedHost()
	// The desktop grew: 1.5 GiB available, 4.35 GiB more from the VM is
	// still under 7.6.
	x.HostAvailable = GiB + GiB/2
	l := (&hostload.Judge{}).Judge(hostReading(x), time.Now())
	if !l.Busy || l.Why != "memory" || hostload.Words(l) != "memory, 1.5 GiB available of the 7.6 GiB it keeps, and the VM can give back 4.4 GiB" {
		t.Errorf("really short: %+v (%s)", l, hostload.Words(l))
	}
	// Stalling, with plenty available.
	x.HostAvailable, x.HostPressure = 12*GiB, 25
	if l := (&hostload.Judge{}).Judge(hostReading(x), time.Now()); !l.Busy || hostload.Words(l) != "memory, 25% stalled" {
		t.Errorf("stalling: %+v (%s)", l, hostload.Words(l))
	}
}

// A VM whose cap is the most there is (MaxCap: the host's memory less its
// reserve), full to its cap as a VM's page cache fills it, beside a desktop
// using an eighth of the host's memory, held every build for good before:
// the host can't have its reserve available then. Now only when what the
// VM's programs use, with the desktop's, leaves the host short. (Not on an
// 8 GiB host, whose 4 GiB reserve is half of it: there a desktop and agents
// using 2 GiB leave it short, cache or not.)
func TestMaxCapDoesNotTripTheGateForGood(t *testing.T) {
	for _, total := range []int64{16 * GiB, 24 * GiB, 31 * GiB, 64 * GiB} {
		most, desktop := MaxCap(total), total/8
		x := sample(most/2, most, 4, 0)
		x.FileCache, x.InactiveFile = most/2-most/16, most/4
		x.Resident = most
		x.HostTotal, x.HostAvailable = total, total-most-desktop
		r := hostReading(x)
		r.Reclaimable = 0
		if l := (&hostload.Judge{}).Judge(r, time.Now()); !l.Busy {
			t.Errorf("%s host, %s cap: the VM's cache didn't trip the old gate: %+v", gib(total), gib(most), l)
		}
		if l := (&hostload.Judge{}).Judge(hostReading(x), time.Now()); l.Busy {
			t.Errorf("%s host, %s cap, half of it cache: held (%s)", gib(total), gib(most), hostload.Words(l))
		}
		// The VM's programs use all but a little of it: really short.
		y := sample(most-GiB/2, most, 4, 0)
		y.FileCache, y.InactiveFile, y.Resident = GiB/4, GiB/8, most
		y.HostTotal, y.HostAvailable = total, total-most-desktop
		if l := (&hostload.Judge{}).Judge(hostReading(y), time.Now()); !l.Busy {
			t.Errorf("%s host, %s cap, all of it in use: not held: %+v", gib(total), gib(most), l)
		}
	}
}

// A sample that is gone (the VM paused, ssh down) says nothing can be given
// back, so the gate goes by the host's own reading alone.
func TestSupervisorReclaimableNeedsAFreshSample(t *testing.T) {
	x, _ := recordedHost()
	s := &supervisor{}
	if got := s.reclaimable(); got != 0 {
		t.Errorf("no sample: %s", gib(got))
	}
	s.sample, s.sampleAt = x, time.Now()
	if got := s.reclaimable(); got != x.reclaimable() || got == 0 {
		t.Errorf("fresh: %s, want %s", gib(got), gib(x.reclaimable()))
	}
	s.sampleAt = time.Now().Add(-memSampleFresh - time.Second)
	if got := s.reclaimable(); got != 0 {
		t.Errorf("stale: %s", gib(got))
	}
}
