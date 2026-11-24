package chv

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/hostload"
)

// These replay what the user's PC went through on 18 Nov 2026, when its
// kernel killed the VM six times in 35 minutes: a 32.7 GB Arch desktop
// (30.4 GiB, which keeps 7.6), a VM of memoryMin 4 GiB and memoryCap 14 GiB,
// and six agents that all resumed their turns at once after each boot. vm.log
// showed "memory: 3.8 GiB → 6.0 GiB (rising 1.0 GiB a second), asked in 0s"
// right after boot, and at 14:42 the VM held 13.8 GiB of the host's memory
// for 4.8 GiB in use, while the host had 6 GiB available: its own programs,
// and what it couldn't reclaim, came to 30.4 − 13.8 − 6 ≈ 10.6 GiB.

const (
	nov18Host = 30*GiB + 410<<20 // 32.7 GB
	// nov18Free is what the host had available with no VM.
	nov18Free = 19*GiB + 820<<20
)

// nov18Busier is how much busier than on 18 Nov the host is made for the
// replays: it kept a quarter (7.6 GiB) available then and keeps an eighth
// (HostKeep, 3.8) now, so a host with that much less free is in the same
// place, the same 12.2 GiB to spare, short and critical at the same sizes of
// the VM.
var nov18Busier = nov18Host/4 - HostKeep(nov18Host)

// simVM is a guest for the policy to size, and the host it runs on: what its
// programs want, its page cache filling what's free of what it has at
// cacheRate, and the host's memory, what it has available being what it has
// without the VM less what the VM holds.
type simVM struct {
	p   memPolicy
	st  memState
	now time.Time

	want, used, cache int64
	total             int64 // plugged
	agents, idle      int
	cacheRate         int64 // a second
	// active is how much of the cache the guest keeps active; 0 for 40% of
	// it.
	active int64
	free   int64 // the host's available memory without the VM

	// What happened.
	grows       []simStep
	shrinks     []simStep
	reclaims    int
	minHostFree int64 // the least the host had available
	maxTotal    int64
	lastD       memDecision
}

type simStep struct {
	at       time.Duration
	from, to int64
}

func newSimVM(p memPolicy, total, used, cache int64) *simVM {
	v := &simVM{p: p, now: time.Unix(1_763_466_000, 0), total: total, want: used, used: used, cache: cache,
		agents: 6, cacheRate: 200 << 20, free: nov18Free - nov18Busier, minHostFree: 1 << 62}
	v.st.Requested = total
	return v
}

func (v *simVM) inactive() int64 {
	if v.active > 0 {
		return max(v.cache-v.active, 0)
	}
	return v.cache * 6 / 10
}

func (v *simVM) resident() int64 { return min(v.used+v.cache, v.total) }

func (v *simVM) hostAvailable() int64 { return v.free - v.resident() }

func (v *simVM) sample() *memSample {
	pressure := 0.0
	if v.want > v.used {
		pressure = 40 // wants more than it has: stalls
	}
	return &memSample{
		Total: v.total, Available: v.total - v.used,
		FileCache: v.cache, InactiveFile: v.inactive(),
		Pressure: pressure, Agents: v.agents, Idle: v.idle,
		Resident: v.resident(), HostTotal: nov18Host, HostAvailable: v.hostAvailable(),
	}
}

// run steps the VM for d from start, a tick at a time, and checks every tick
// with check, when set.
func (v *simVM) run(t *testing.T, start, d time.Duration, check func(at time.Duration, v *simVM)) {
	t.Helper()
	for at := start; at < start+d; at += memTick {
		dt := memTick.Seconds()
		// The guest: its programs take what they want of what it has, and its
		// cache what's left, as builds read.
		v.used = min(v.want, v.total-256<<20)
		v.cache = min(v.cache+int64(float64(v.cacheRate)*dt), max(v.total-v.used-256<<20, 0))
		d := v.p.decide(&v.st, v.sample(), v.now.Add(at))
		v.lastD = d
		if d.Reclaim {
			v.reclaims++
			v.cache = 0
		}
		if d.Trim > 0 {
			v.cache = max(v.cache-d.Trim, 0)
		}
		if d.Target > 0 {
			step := simStep{at, v.total, d.Target}
			if d.Target > v.total {
				v.grows = append(v.grows, step)
			} else {
				v.shrinks = append(v.shrinks, step)
				// Unplugging evicts what's in the blocks that go.
				v.cache = min(v.cache, max(d.Target-v.used, 0))
			}
			v.total = d.Target
		}
		v.maxTotal = max(v.maxTotal, v.total)
		v.minHostFree = min(v.minHostFree, v.hostAvailable())
		if check != nil {
			check(at, v)
		}
	}
}

func (v *simVM) String() string {
	return fmt.Sprintf("plugged %s, using %s, caching %s, holding %s of the host, which has %s available",
		gib(v.total), gib(v.used), gib(v.cache), gib(v.resident()), gib(v.hostAvailable()))
}

// After a boot every agent resumed its turn at once and the guest's use rose
// a GiB a second, from 3.8 to 9.5 GiB, and the VM followed it at once to its
// cap. Now it grows half a GiB a second at most, and never past what the host
// can spare: 19.8 GiB available without the VM, less the 7.6 it keeps, is
// 12.2. Its cache fills whatever it's given, and the host keeps its reserve.
func TestRecordedBootBurstStopsAtTheHostsCeiling(t *testing.T) {
	reserve := HostKeep(nov18Host)
	v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 14 * GiB}, 4*GiB, 3*GiB+800<<20, 0)
	v.run(t, 0, 10*time.Minute, func(at time.Duration, v *simVM) {
		if at < 6*time.Second {
			v.want = min(3*GiB+800<<20+int64(at.Seconds()*float64(GiB)), 9*GiB+GiB/2)
		}
	})
	if v.maxTotal > 12*GiB {
		t.Errorf("grew to %s, past the 12.2 GiB the host can spare", gib(v.maxTotal))
	}
	if v.minHostFree < reserve {
		t.Errorf("the host fell to %s available, below the %s it keeps", gib(v.minHostFree), gib(reserve))
	}
	if v.total != 12*GiB {
		t.Errorf("settled at %s, want the 12 GiB the host can spare (%v)", gib(v.total), v)
	}
	last := -memGrowEvery
	for _, g := range v.grows {
		if g.to-g.from > memGrowMax || g.at-last < memGrowEvery {
			t.Errorf("grew %s → %s at %s, %s after the last: want at most %s, %s apart", gib(g.from), gib(g.to), g.at, g.at-last, gib(memGrowMax), memGrowEvery)
		}
		last = g.at
	}
	if len(v.grows) == 0 || v.grows[0].to != 5*GiB {
		t.Errorf("grows %v: want the first to 5 GiB, a GiB above what it boots with", v.grows)
	}
}

// At 14:42: 14 GiB plugged, 13.8 held of the host, 4.8 in use, the rest
// cache, and the host at 6 GiB available of the 7.6 it kept (here, 0.4 GiB
// under the 3.8 it keeps now, above hostCriticalAt). It never gave
// any of it back: refaults had raised its cache floor to half the cap, which
// kept the target at the cap, and held trims off. Now the host comes first:
// with trims held off, it shrinks within seconds to its use, the cache
// allowance and 2 GiB; with them not, a trim gives the host back its reserve
// at once, and it shrinks to what the host can spare. Either way it grows
// back as soon as the host has its reserve again, never further than the
// host can spare, so the host keeps its reserve from then on. Boots aren't
// held: 6 GiB is far over the host's hard floor.
func TestRecordedVMGivesBackWhatTheHostIsShortOf(t *testing.T) {
	reserve := HostKeep(nov18Host)
	used := 4*GiB + 800<<20
	// What the host has without the VM, and so what it can spare.
	free := 14*GiB - 200<<20 + reserve - 400<<20
	spare := (free - reserve) / memGranule * memGranule
	for _, tc := range []struct {
		name       string
		refaulting bool
		shrinkTo   int64
	}{
		{"refaults holding trims off", true, 8 * GiB}, // 4.8 used, 1.2 of cache, 2 of headroom
		{"trims", false, 0},                           // to what the host can spare
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 14 * GiB}, 14*GiB, used, 9*GiB)
			v.free = free
			v.cacheRate = 50 << 20
			if tc.shrinkTo == 0 {
				tc.shrinkTo = spare
			}
			if tc.refaulting {
				v.st.floor = 7 * GiB
				v.st.noTrimUntil = v.now.Add(time.Hour)
			}
			if got := v.hostAvailable(); got >= reserve || got < hostCriticalAt(nov18Host) {
				t.Fatalf("the replay starts with the host at %s available, want short of %s but above %s", gib(got), gib(reserve), gib(hostCriticalAt(nov18Host)))
			}
			var recovered time.Duration = -1
			var heldAt []time.Duration
			v.run(t, 0, 5*time.Minute, func(at time.Duration, v *simVM) {
				if recovered < 0 && v.hostAvailable() >= reserve {
					recovered = at
				}
				if recovered >= 0 && v.hostAvailable() < reserve {
					t.Fatalf("at %s, after recovering, the host is short again: %v", at, v)
				}
				if v.st.HoldBoots {
					heldAt = append(heldAt, at)
				}
				if v.st.Ceiling > spare {
					t.Fatalf("at %s the daemon is told the VM may grow to %s", at, gib(v.st.Ceiling))
				}
				if at > memShrinkAfterHost && v.total > spare {
					t.Fatalf("at %s: %v, past the %s the host can spare", at, v, gib(spare))
				}
			})
			if len(v.shrinks) == 0 || v.shrinks[0].at > memShrinkAfterHost || v.shrinks[0].to != tc.shrinkTo {
				t.Fatalf("shrinks %v: want one to %s within %s", v.shrinks, gib(tc.shrinkTo), memShrinkAfterHost)
			}
			if recovered < 0 || recovered > memShrinkAfterHost+memTick {
				t.Errorf("the host had its reserve again at %s", recovered)
			}
			if len(heldAt) != 0 {
				t.Errorf("boots held at %v, with the host over its hard floor", heldAt)
			}
			// Grown back, if at all, by steps, to what the host can spare.
			if v.total != spare {
				t.Errorf("ended %v, want %s plugged", v, gib(spare))
			}
		})
	}
}

// The desktop takes 3 GiB more (a game, a browser full of tabs) while the
// VM sits at all the host could spare: the VM gives it back within seconds,
// and never grows past the lower ceiling after.
func TestDesktopGrowingShrinksTheVM(t *testing.T) {
	reserve := HostKeep(nov18Host)
	v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 14 * GiB}, 12*GiB, 4*GiB+800<<20, 7*GiB)
	v.run(t, 0, time.Minute, nil)
	if v.total != 12*GiB || v.hostAvailable() < reserve {
		t.Fatalf("before: %v", v)
	}
	v.free -= 3 * GiB
	if v.hostAvailable() >= reserve {
		t.Fatalf("the desktop's 3 GiB left the host its reserve: %v", v)
	}
	v.shrinks = nil
	var short time.Duration
	v.run(t, time.Minute, 5*time.Minute, func(at time.Duration, v *simVM) {
		if v.hostAvailable() < reserve {
			short = at - time.Minute
		}
		if at > 2*time.Minute && v.total > 9*GiB {
			t.Fatalf("at %s: %v, past the 9.2 GiB the host can spare now", at, v)
		}
	})
	if short > memShrinkAfterHost+memTick {
		t.Errorf("the host was short until %s after the desktop grew", short)
	}
	if len(v.shrinks) == 0 || v.total != 9*GiB {
		t.Errorf("shrinks %v, ended %v: want back down, then up to 9 GiB", v.shrinks, v)
	}
}

// Down to hostCriticalAt the VM doesn't wait: on the same sample it drops
// its caches and shrinks to its use and memHeadroomLeast.
func TestHostNearlyOutShrinksAtOnce(t *testing.T) {
	v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 14 * GiB}, 12*GiB, 4*GiB+800<<20, 7*GiB)
	v.free = 12*GiB + 2*GiB + GiB/2 // 2.5 GiB available of the 3.8 it keeps, under hostCriticalAt's 2.9
	v.run(t, 0, memTick, nil)
	if d := v.lastD; !d.Shrink || !d.Reclaim || d.Target != 6*GiB {
		t.Fatalf("decide = %+v, want a shrink to 6 GiB (4.8 in use and 1 GiB) with the caches dropped", d)
	}
}

// While the host has less than it keeps, the VM doesn't grow; the moment it
// has it again, it does, a GiB at a time, with no wait for the host to have
// stayed calm (24 Nov 2026: a 30.5 GiB host at 7.5 GiB available, against a
// reserve of a quarter, turned "busy" every minute and held the VM at 7 GiB).
func TestHostShortHoldsGrowthOnlyWhileItIs(t *testing.T) {
	v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 14 * GiB}, 4*GiB, 3*GiB, 0)
	v.want = 6 * GiB
	room := v.free
	v.free = v.resident() + HostKeep(nov18Host) - GiB
	v.run(t, 0, time.Minute, nil)
	if len(v.grows) > 0 {
		t.Fatalf("grew while the host was short: %v", v.grows)
	}
	v.free = room
	v.run(t, time.Minute, memTick, nil)
	if len(v.grows) != 1 || v.grows[0].to != 5*GiB {
		t.Errorf("grows %v, want one to 5 GiB on the first sample with the host over what it keeps", v.grows)
	}
}

// A host at 7.5 GiB available of 30.5, its desktop calm, gives the VM what it
// asks for, a GiB at a time from the first sample, up to what it can spare
// beyond what it keeps: under a reserve of a quarter (7.6 GiB) it was
// "busy", and the VM sat at 7 GiB.
func TestHostAtTheOldReserveLetsTheVMGrow(t *testing.T) {
	v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 22 * GiB}, 7*GiB, 6*GiB, 0)
	v.want = 10 * GiB
	v.free = v.resident() + 15*GiB/2
	spare := (v.free - HostKeep(nov18Host)) / memGranule * memGranule
	v.run(t, 0, 30*time.Second, func(at time.Duration, v *simVM) {
		if v.hostAvailable() < HostKeep(nov18Host) {
			t.Fatalf("at %s the host is short: %v", at, v)
		}
	})
	if len(v.grows) == 0 || v.grows[0].at != 0 || v.total != spare {
		t.Errorf("grows %v, ended %v: want grown from the first sample to the %s the host can spare", v.grows, v, gib(spare))
	}
}

// Every agent idle for memIdleAfterAll (waiting on the user, by the
// daemon): the VM gives back its cache beyond the allowance, and its caches
// are dropped once, though the host has plenty. Before, idle agents' small
// headroom already went, but the cache floor stayed.
func TestIdleVMGivesBack(t *testing.T) {
	v := newSimVM(memPolicy{Min: 4 * GiB, Cap: 14 * GiB}, 12*GiB, 4*GiB, 6*GiB)
	v.free = 28 * GiB
	v.idle = v.agents
	v.cacheRate = 0
	v.active = 2*GiB + 400<<20
	v.run(t, 0, memIdleAfterAll-memTick, nil)
	// 4 in use, the 2.4 GiB of active cache it keeps, 2 of headroom.
	if v.total != 8*GiB+GiB/2 || v.reclaims > 0 {
		t.Fatalf("idle for less than %s: %v, reclaims %d; want 8.5 GiB and none", memIdleAfterAll, v, v.reclaims)
	}
	v.run(t, memIdleAfterAll-memTick, 3*memShrinkAfter, nil)
	// The caches dropped: 4 in use and 2 of headroom for six idle agents,
	// a minute a step.
	if v.total != 6*GiB || v.reclaims != 1 {
		t.Errorf("idle: shrinks %v, reclaims %d, %v; want down to 6 GiB and one reclaim", v.shrinks, v.reclaims, v)
	}
}

// In safe mode, after a crash loop, the VM grows half a GiB at a time and
// keeps only 2 GiB beyond use, however many agents are busy.
func TestSafeModeGrowsSlowly(t *testing.T) {
	start := time.Unix(1_763_466_000, 0)
	p := memPolicy{Min: 4 * GiB, Cap: 14 * GiB, SafeUntil: start.Add(safeFor)}
	v := newSimVM(p, 4*GiB, 3*GiB+800<<20, 0)
	v.free = 28 * GiB
	v.cacheRate = 0
	v.want = 6 * GiB
	v.run(t, 0, time.Minute, nil)
	for _, g := range v.grows {
		if g.to-g.from > memSafeGrowMax {
			t.Errorf("grew %s → %s in safe mode", gib(g.from), gib(g.to))
		}
	}
	if v.total != 8*GiB {
		t.Errorf("safe mode: %v; want 8 GiB, 6 in use and 2 beyond", v)
	}
	// Once it's over, the busy agents' headroom comes back.
	v.run(t, safeFor, time.Minute, nil)
	if v.total != 12*GiB {
		t.Errorf("after safe mode: %v; want 12 GiB, 6 in use and a GiB for each busy agent", v)
	}
}

// Under its floor (2 GiB of the 30.4) for memEmergencyAfter, the host has an
// agent stopped, one every memShedEvery while it lasts, and none once it's
// over; a dip shorter than that stops none.
func TestHostUnderItsFloorShedsAgents(t *testing.T) {
	if f := hostFloor(nov18Host); f != 2*GiB {
		t.Fatalf("hostFloor = %s, want 2 GiB", gib(f))
	}
	if f := hostFloor(64 * GiB); f < 3*GiB+800<<20 || f > 4*GiB {
		t.Fatalf("hostFloor(64 GiB) = %s, want 6%%", gib(f))
	}
	p := memPolicy{Min: 4 * GiB, Cap: 14 * GiB}
	start := time.Unix(1_763_466_000, 0)
	x := &memSample{Total: 12 * GiB, Available: 2 * GiB, Agents: 6, Resident: 12 * GiB, HostTotal: nov18Host}
	var sheds []time.Duration
	run := func(st *memState, from, d time.Duration, avail int64) {
		x.HostAvailable = avail
		for at := from; at < from+d; at += memTick {
			if dec := p.decide(st, x, start.Add(at)); dec.Shed {
				sheds = append(sheds, at)
				if !strings.Contains(dec.ShedReason, "under the 2.0 GiB") {
					t.Errorf("reason %q", dec.ShedReason)
				}
			}
		}
	}
	// A dip of 2 s: none.
	var st memState
	st.Requested = 12 * GiB
	run(&st, 0, 2*time.Second, GiB)
	run(&st, 2*time.Second, time.Minute, 3*GiB)
	if len(sheds) > 0 {
		t.Fatalf("stopped agents at %v for a 2 s dip", sheds)
	}
	// 45 s under it: at 3 s, 23 s and 43 s.
	st = memState{Requested: 12 * GiB}
	run(&st, 0, 45*time.Second, GiB+GiB/2)
	want := []time.Duration{memEmergencyAfter, memEmergencyAfter + memShedEvery, memEmergencyAfter + 2*memShedEvery}
	if !slices.Equal(sheds, want) {
		t.Errorf("stopped agents at %v, want %v", sheds, want)
	}
	// Over it again: no more.
	sheds = nil
	run(&st, 45*time.Second, time.Minute, 3*GiB)
	if len(sheds) > 0 {
		t.Errorf("stopped agents at %v with the host over its floor", sheds)
	}
}

// On 19 Nov 2026 the VM held 10 GiB of a 30.5 GiB host that had 6.4 GiB
// available, and its agents needed far more: keeping a quarter (7.6 GiB),
// the host let it grow by nothing and took memory back. Keeping an eighth,
// it grows by what the host has beyond that, a GiB at a time, and the app is
// told the host is what holds it there.
func TestNov19VMGrowsIntoWhatTheHostCanSpare(t *testing.T) {
	const host = 30*GiB + GiB/2
	keep := HostKeep(host)
	if keep >= 6*GiB+400<<20 {
		t.Fatalf("HostKeep(%s) = %s: the VM still couldn't grow", gib(host), gib(keep))
	}
	p := memPolicy{Min: 4 * GiB, Cap: 22 * GiB}
	x := sample(9*GiB+GiB/2, 10*GiB, 6, 0)
	x.Resident = 10 * GiB
	x.HostTotal, x.HostAvailable = host, 6*GiB+400<<20
	st := memState{Requested: 10 * GiB}
	got, targets := settle(p, &st, x, time.Now(), time.Minute)
	spare := (10*GiB + 6*GiB + 400<<20 - keep) / memGranule * memGranule
	if got != spare {
		t.Errorf("grew to %s (%v), want the %s the host can spare", gib(got), targets, gib(spare))
	}
	if x.HostAvailable < keep {
		t.Errorf("the host was left %s, under the %s it keeps", gib(x.HostAvailable), gib(keep))
	}
	if st.Wanted <= got {
		t.Errorf("Wanted = %s at %s: the app isn't told the host holds it back", gib(st.Wanted), gib(got))
	}
}

// Once the host isn't what holds the VM back, nothing says it is.
func TestWantedOnlyWhileTheHostHolds(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 12 * GiB}
	x := sample(3*GiB, 8*GiB, 8, 0)
	x.Resident = 8 * GiB
	x.HostTotal, x.HostAvailable = 32*GiB, 20*GiB
	st := memState{Requested: 8 * GiB}
	if _, _ = settle(p, &st, x, time.Now(), 30*time.Second); st.Wanted != 0 {
		t.Errorf("plenty on the host: Wanted = %s", gib(st.Wanted))
	}
	// At its cap, with agents wanting more: the cap holds it, not the host.
	x = sample(10*GiB, 12*GiB, 8, 0)
	x.Resident = 12 * GiB
	x.HostTotal, x.HostAvailable = 32*GiB, 15*GiB
	st = memState{Requested: 12 * GiB}
	if _, _ = settle(p, &st, x, time.Now(), 30*time.Second); st.Wanted != 0 {
		t.Errorf("at its cap: Wanted = %s", gib(st.Wanted))
	}
}

// The live reserve: an eighth of the host, 3 GiB at least, and always above
// hostCriticalAt, which is always above the floor where agents are stopped.
func TestHostKeep(t *testing.T) {
	for _, tc := range []struct{ total, keep int64 }{
		{8 * GiB, 3 * GiB},
		{16 * GiB, 3 * GiB},
		{32 * GiB, 4 * GiB},
		{64 * GiB, 8 * GiB},
	} {
		if got := HostKeep(tc.total); got != tc.keep {
			t.Errorf("HostKeep(%s) = %s, want %s", gib(tc.total), gib(got), gib(tc.keep))
		}
	}
	for total := int64(4 * GiB); total <= 256*GiB; total += GiB {
		keep, crit, floor := HostKeep(total), hostCriticalAt(total), hostFloor(total)
		if keep <= crit || crit <= floor || HostReserve(total) < floor {
			t.Errorf("%s: keeps %s, critical at %s, floor %s, reserve %s: want keep > critical > floor, reserve ≥ floor",
				gib(total), gib(keep), gib(crit), gib(floor), gib(HostReserve(total)))
		}
	}
}

// Boots are held only while the host is below its hard floor, the larger of
// 2 GiB and 6% of it, and from the moment it is over it again. On 19 Nov
// 2026 the host had 8.2 of its 30.5 GiB free, under its 7.6 GiB reserve
// within two minutes, and every boot was held while its own memory pressure
// read 0.
func TestBootsAreHeldOnlyBelowTheHostsHardFloor(t *testing.T) {
	host := 61 * GiB / 2
	var st memState
	now := time.Now()
	for _, c := range []struct {
		available int64
		want      bool
	}{
		{82 * GiB / 10, false}, // 19 Nov 2026: under its reserve
		{3 * GiB, false},
		{3 * GiB / 2, true}, // under 2 GiB
		{21 * GiB / 10, false},
	} {
		now = now.Add(memTick)
		st.observeHost(memSample{HostTotal: host, HostAvailable: c.available}, now)
		if st.HoldBoots != c.want {
			t.Errorf("host at %s of %s: HoldBoots = %v, want %v", gib(c.available), gib(host), st.HoldBoots, c.want)
		}
	}
	big := int64(64 * GiB)
	if want := int64(hostload.FloorShare * float64(big)); hostFloor(big) != want {
		t.Errorf("a 64 GiB host's floor is %s, want 6%% of it", gib(hostFloor(big)))
	}
}
