package chv

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func sample(used, total int64, agents int, pressure float64) *memSample {
	return &memSample{Total: total, Available: total - used, Agents: agents, Pressure: pressure}
}

func TestMemPolicyTarget(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	for _, tc := range []struct {
		name         string
		used         int64
		agents, idle int
		want         int64
	}{
		{"idle stays at the minimum", GiB / 2, 0, 0, 4 * GiB},
		{"two GiB of headroom at least", 3 * GiB, 1, 0, 5 * GiB},
		{"a GiB of headroom a busy agent", 3 * GiB, 4, 0, 7 * GiB},
		{"a quarter of one an idle agent", 3 * GiB, 8, 4, 8 * GiB},
		{"idle agents within the minimum", 3 * GiB, 6, 6, 5 * GiB},
		{"more idle than running counts none busy", 3 * GiB, 2, 5, 5 * GiB},
		{"rounded up to 512 MiB", 3*GiB + 1, 0, 0, 5*GiB + 512<<20},
		{"never above the cap", 20 * GiB, 10, 0, 16 * GiB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := sample(tc.used, 16*GiB, tc.agents, 0)
			x.Idle = tc.idle
			if got := p.target(*x, 0); got != tc.want {
				t.Errorf("target = %s, want %s", gib(got), gib(tc.want))
			}
		})
	}
}

func TestMemPolicyGrowsAtOnce(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	st := memState{Requested: 4 * GiB}
	now := time.Now()
	if d := p.decide(&st, nil, now); d.Target != 0 {
		t.Fatalf("no sample: %+v, want nothing", d)
	}
	d := p.decide(&st, sample(3*GiB, 4*GiB, 2, 0), now)
	if d.Target != 5*GiB || d.Shrink {
		t.Fatalf("decide = %+v, want a grow to 5 GiB", d)
	}
	if st.Requested != 5*GiB {
		t.Errorf("Requested = %s", gib(st.Requested))
	}
	if d := p.decide(&st, sample(3*GiB, 5*GiB, 2, 0), now.Add(memTick)); d.Target != 0 {
		t.Errorf("steady: %+v, want nothing", d)
	}
}

func TestMemPolicyGrowsUnderPressure(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	st := memState{Requested: 8 * GiB}
	now := time.Now()
	half := GiB / 2
	// Stalls with plenty available aren't for want of memory the VM can be
	// given (the kernel's caches can't use hotplugged memory): nothing.
	if d := p.decide(&st, sample(2*GiB, 8*GiB, 0, 25), now); d.Target != 0 {
		t.Fatalf("stalls with 6 GiB available: %+v, want nothing", d)
	}
	// Short, and stalling: a step more than the numbers alone ask for.
	d := p.decide(&st, sample(6*GiB+half, 8*GiB, 0, 25), now)
	if d.Target != 10*GiB {
		t.Fatalf("decide = %+v, want a grow to 10 GiB", d)
	}
	// Not again until the grow has had time to help (the guest hasn't seen
	// all of it yet).
	if d := p.decide(&st, sample(6*GiB+half, 8*GiB, 0, 25), now.Add(memTick)); d.Target != 0 {
		t.Errorf("within the cooldown: %+v, want nothing", d)
	}
	if d := p.decide(&st, sample(6*GiB+half, 8*GiB, 0, 25), now.Add(memGrowCooldown)); d.Target != 12*GiB {
		t.Errorf("after the cooldown: %+v, want 12 GiB", d)
	}
	st.Requested = 16 * GiB
	if d := p.decide(&st, sample(15*GiB, 16*GiB, 0, 50), now.Add(time.Minute)); d.Target != 0 {
		t.Errorf("at the cap: %+v, want nothing", d)
	}
}

func TestMemPolicyShrinksLate(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	st := memState{Requested: 12 * GiB}
	start := time.Now()
	at := func(d time.Duration) time.Time { return start.Add(d) }
	if d := p.decide(&st, sample(GiB, 12*GiB, 0, 0), at(0)); d.Target != 0 {
		t.Fatalf("shrank at once: %+v", d)
	}
	// Use rises for a while, still well below: the shrink will be to the
	// highest target seen meanwhile, 6 GiB.
	p.decide(&st, sample(4*GiB, 12*GiB, 0, 0), at(20*time.Second))
	if d := p.decide(&st, sample(GiB, 12*GiB, 0, 0), at(memShrinkAfter-time.Second)); d.Target != 0 {
		t.Fatalf("shrank before %s: %+v", memShrinkAfter, d)
	}
	d := p.decide(&st, sample(GiB, 12*GiB, 0, 0), at(memShrinkAfter))
	if d.Target != 6*GiB || !d.Shrink {
		t.Fatalf("decide = %+v, want a shrink to 6 GiB", d)
	}
	if st.Requested != 6*GiB || !st.lowSince.IsZero() {
		t.Errorf("state after the shrink: %+v", st)
	}
}

func TestMemPolicyShrinkWaitsAgainAfterABump(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	st := memState{Requested: 12 * GiB}
	start := time.Now()
	p.decide(&st, sample(GiB, 12*GiB, 0, 0), start)
	// Back to needing about what it has: the wait starts over.
	p.decide(&st, sample(10*GiB, 12*GiB, 0, 0), start.Add(30*time.Second))
	if d := p.decide(&st, sample(GiB, 12*GiB, 0, 0), start.Add(memShrinkAfter)); d.Target != 0 {
		t.Fatalf("shrank though the wait had started over: %+v", d)
	}
	if d := p.decide(&st, sample(GiB, 12*GiB, 0, 0), start.Add(memShrinkAfter*2)); d.Target != 4*GiB {
		t.Fatalf("decide = %+v, want a shrink to 4 GiB", d)
	}
}

func TestMemPolicyIgnoresSmallShrinks(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	st := memState{Requested: 6 * GiB}
	start := time.Now()
	for i := range 100 {
		// 5.5 GiB is less than a GiB below: not worth it.
		if d := p.decide(&st, sample(3*GiB+GiB/2, 6*GiB, 0, 0), start.Add(time.Duration(i)*memTick)); d.Target != 0 {
			t.Fatalf("tick %d: %+v", i, d)
		}
	}
}

func TestParseMemSample(t *testing.T) {
	out := `MemTotal:        8024064 kB
MemFree:         6000000 kB
MemAvailable:    7000000 kB
Buffers:           10000 kB
SwapTotal:       8388604 kB
SwapFree:        8000000 kB
Active(file):    1000000 kB
Inactive(file):   500000 kB
some avg10=12.50 avg60=3.00 avg300=1.00 total=123
full avg10=1.00 avg60=0.00 avg300=0.00 total=12
auto_online online_movable
agents 3
`
	s, err := parseMemSample(out)
	if err != nil {
		t.Fatal(err)
	}
	want := memSample{Total: 8024064 << 10, Available: 7000000 << 10, SwapTotal: 8388604 << 10, SwapFree: 8000000 << 10, Pressure: 12.5, Agents: 3, AutoOnline: "online_movable",
		FileCache: 1500000 << 10, InactiveFile: 500000 << 10}
	if s != want {
		t.Errorf("parseMemSample = %+v, want %+v", s, want)
	}
	if _, err := parseMemSample("sh: 1: cat: not found\n"); err == nil {
		t.Error("parsed a sample out of nothing")
	}
}

// A burst gets its memory ahead of it: headroom for what use would reach in
// memLookahead at the rate it's rising, remembered through a flat sample.
func TestMemPolicyGrowsAheadOfABurst(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	st := memState{Requested: 4 * GiB}
	now := time.Now()
	p.decide(&st, sample(GiB, 4*GiB, 1, 0), now)
	// 1 GiB in half a second: 2 GiB a second, so 6 GiB ahead of 2 GiB used.
	d := p.decide(&st, sample(2*GiB, 4*GiB, 1, 0), now.Add(memTick))
	if d.Target != 8*GiB || !strings.Contains(d.Reason, "rising") {
		t.Fatalf("decide = %+v, want 8 GiB for a rise of 2 GiB a second", d)
	}
	// A flat sample within memRateWindow keeps the headroom.
	if d := p.decide(&st, sample(2*GiB, 8*GiB, 1, 0), now.Add(2*memTick)); d.Target != 0 || st.rate < float64(GiB) {
		t.Errorf("flat sample: %+v, rate %.0f", d, st.rate)
	}
	// Once the burst is over, the rate goes back to what it is.
	for i := 3; i < 10; i++ {
		p.decide(&st, sample(2*GiB, 8*GiB, 1, 0), now.Add(time.Duration(i)*memTick))
	}
	if st.rate != 0 {
		t.Errorf("rate after the burst = %.0f, want 0", st.rate)
	}
}

// A quiet VM whose host memory is mostly cache is told to reclaim it, once
// in memReclaimEvery; a busy one, or one that holds no more than it uses,
// isn't.
func TestMemPolicyReclaimsWhenQuiet(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	st := memState{Requested: 4 * GiB}
	start := time.Now()
	s := func(agents int, resident int64, busy uint64, i int) *memSample {
		x := sample(GiB, 4*GiB, agents, 0)
		x.Resident = resident
		x.CPUTotal = uint64(i) * 100
		x.CPUBusy = uint64(i) * busy
		return x
	}
	var reclaims []int
	for i := range 200 { // 100 s
		// Agents run for the first 20 s, then none do.
		agents := 0
		if i < 40 {
			agents = 1
		}
		if d := p.decide(&st, s(agents, 3*GiB+GiB/2, 50, i), start.Add(time.Duration(i)*memTick)); d.Reclaim {
			reclaims = append(reclaims, i)
			if !strings.Contains(d.Reason, "quiet") {
				t.Errorf("reason %q", d.Reason)
			}
		}
	}
	// 30 s after the last agent stopped: sample 40 + 60.
	if len(reclaims) != 1 || reclaims[0] != 100 {
		t.Errorf("reclaimed at samples %v, want [100]", reclaims)
	}

	// An agent that runs but idles: its CPUs are quiet for memIdleAfter,
	// once the smoothed use has come down (about 75 s).
	st = memState{Requested: 4 * GiB}
	reclaims = nil
	for i := range 600 { // 300 s
		if d := p.decide(&st, s(1, 3*GiB, 1, i), start.Add(time.Duration(i)*memTick)); d.Reclaim {
			reclaims = append(reclaims, i)
		}
	}
	if len(reclaims) != 1 {
		t.Errorf("idle agent: reclaimed at samples %v, want once", reclaims)
	}

	// Busy, or holding little more than it uses: never.
	for _, tc := range []struct {
		name     string
		resident int64
		busy     uint64
	}{{"busy", 8 * GiB, 60}, {"lean", GiB + GiB/2, 1}, {"unknown", 0, 1}} {
		st = memState{Requested: 4 * GiB}
		for i := range 400 {
			if d := p.decide(&st, s(1, tc.resident, tc.busy, i), start.Add(time.Duration(i)*memTick)); d.Reclaim {
				t.Errorf("%s: reclaimed at sample %d", tc.name, i)
				break
			}
		}
	}
}

// A busy VM's file cache is trimmed, a bounded amount of its inactive part at
// a time, when the host is short of memory or holds cache beyond use and the
// allowance; never while neither is so.
func TestMemPolicyTrimsWhileBusy(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	busy := func(cache, inactive int64, hostAvail int64, hostPSI float64) *memSample {
		x := sample(8*GiB, 16*GiB, 3, 0)
		x.Available = 16*GiB - 8*GiB
		x.FileCache, x.InactiveFile = cache, inactive
		x.Resident = 15 * GiB
		x.HostTotal, x.HostAvailable, x.HostPressure = 32*GiB, hostAvail, hostPSI
		return x
	}
	run := func(s *memSample, ticks int) (trims []memDecision) {
		st := memState{Requested: 11 * GiB} // what it uses, plus headroom for 3 agents
		for i := range ticks {
			x := *s
			x.CPUTotal, x.CPUBusy = uint64(i)*100, uint64(i)*90
			if d := p.decide(&st, &x, start.Add(time.Duration(i)*memTick)); d.Trim > 0 {
				if d.Reclaim || d.Shrink {
					t.Errorf("a trim with a reclaim or a shrink: %+v", d)
				}
				trims = append(trims, d)
			}
		}
		return trims
	}

	// The host is stalling: up to memTrimMax every memTrimEvery.
	trims := run(busy(7*GiB, 5*GiB, 8*GiB, 25), 240) // 120 s
	if len(trims) != 4 || trims[0].Trim != memTrimMax || !trims[0].HostShort || !strings.Contains(trims[0].Reason, "stalled") {
		t.Errorf("host stalling: %+v", trims)
	}
	// The host has little available: the inactive part, when it's less.
	trims = run(busy(3*GiB, GiB, 2*GiB, 0), 2)
	if len(trims) != 1 || trims[0].Trim != GiB || !trims[0].HostShort {
		t.Errorf("host short: %+v", trims)
	}
	// The host is fine but the cache holds 7 GiB beside 8 in use, where 2
	// are kept: up to memTrimMax every memTrimIdleEvery, without dropping
	// the host's cache.
	trims = run(busy(7*GiB, 5*GiB, 20*GiB, 0), 480) // 240 s
	if len(trims) != 2 || trims[0].Trim != memTrimMax || trims[0].HostShort || !strings.Contains(trims[0].Reason, "kept beside") {
		t.Errorf("big cache: %+v, want 2 of %s", trims, gib(memTrimMax))
	}
	// Neither: never. Nor with too little inactive cache to bother, nor
	// cache this machine doesn't hold.
	notHeld := busy(7*GiB, 5*GiB, 20*GiB, 0)
	notHeld.Resident = 9 * GiB
	for name, s := range map[string]*memSample{
		"fine":         busy(2*GiB, GiB, 20*GiB, 0),
		"all active":   busy(7*GiB, 100<<20, 2*GiB, 30),
		"host unknown": busy(2*GiB, GiB, 0, 0),
		"not held":     notHeld,
	} {
		if s.HostAvailable == 0 {
			s.HostTotal = 0
		}
		if trims := run(s, 240); len(trims) > 0 {
			t.Errorf("%s: trimmed %+v", name, trims)
		}
	}
}

func TestPSISome(t *testing.T) {
	if got := psiSome("some avg10=3.25 avg60=1.00 avg300=0.00 total=1\nfull avg10=1.00 avg60=0 avg300=0 total=0\n"); got != 3.25 {
		t.Errorf("psiSome = %v", got)
	}
	if got := psiSome(""); got != 0 {
		t.Errorf("psiSome of nothing = %v", got)
	}
}

func TestTrimScript(t *testing.T) {
	if got := trimScript(2 * GiB); !strings.Contains(got, "echo '2147483648 swappiness=0' | sudo -n tee /sys/fs/cgroup/memory.reclaim") || !strings.Contains(got, "compact_memory") {
		t.Errorf("trimScript = %q", got)
	}
}

// The numbers from a real host (Linux, nightly .134): the VM plugged to
// 13.5 GiB while its programs use 7 (anon 6.3, kernel 0.5), six agents
// running, 4.5 GiB of page cache filling the headroom, and the host at 73%
// used, so not short. Before, the headroom was a GiB for each of the six, and
// a cache under a third of the VM was never trimmed: the host held 13 GiB.
// Now the cache beyond use and its allowance is trimmed, and idle agents keep
// little headroom; growth stays instant once they're busy again.
func TestMemPolicyRealHostHeadroom(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	host := func(idle int) *memSample {
		x := sample(7*GiB, 13*GiB+GiB/2, 6, 0)
		x.Idle = idle
		x.FileCache, x.InactiveFile = 4*GiB+GiB/2, 3*GiB
		x.Resident = 13 * GiB
		x.HostTotal, x.HostAvailable = 32*GiB, 32*GiB*27/100
		return x
	}
	// replay runs the policy for ticks samples, with the guest doing what it's
	// told: a trim drops that much inactive cache and the compaction after it
	// hands every free page back, so the host holds use and cache; a shrink
	// unplugs, and its reclaim drops the cache.
	replay := func(x *memSample, st *memState, ticks int) (trims []int64, shrinks []int64) {
		for i := range ticks {
			x.CPUTotal, x.CPUBusy = uint64(i)*100, uint64(i)*60
			d := p.decide(st, x, start.Add(time.Duration(i)*memTick))
			if d.Trim > 0 {
				trims = append(trims, d.Trim)
				x.FileCache -= d.Trim
				x.InactiveFile -= d.Trim
				x.Resident = x.Used() + x.FileCache
			}
			if d.Shrink {
				shrinks = append(shrinks, d.Target)
				x.Available -= x.Total - d.Target
				x.Total = d.Target
				x.FileCache, x.InactiveFile = 0, 0
				x.Resident = x.Used()
			} else if d.Target > 0 {
				x.Available += d.Target - x.Total
				x.Total = d.Target
			}
		}
		return trims, shrinks
	}

	// All six busy: the headroom stays (13 GiB), but the cache in it goes,
	// down to the 1.75 GiB kept beside 7 in use, in two trims.
	x, st := host(0), memState{Requested: 13*GiB + GiB/2}
	trims, shrinks := replay(x, &st, 600) // 5 min
	if want := []int64{2 * GiB, 3 * GiB / 4}; !slices.Equal(trims, want) || len(shrinks) > 0 {
		t.Errorf("all busy: trims %v, shrinks %v; want trims %v and no shrink", trims, shrinks, want)
	}
	if want := 8*GiB + 3*GiB/4; x.Resident != want {
		t.Errorf("all busy: the host holds %s, want %s", gib(x.Resident), gib(want))
	}

	// Two busy and four idle: 3 GiB of headroom, so the VM shrinks to 10 GiB
	// once that's lasted memShrinkAfter, and the host holds only use.
	x, st = host(4), memState{Requested: 13*GiB + GiB/2}
	_, shrinks = replay(x, &st, 600)
	if !slices.Equal(shrinks, []int64{10 * GiB}) || x.Resident != 7*GiB {
		t.Errorf("four idle: shrinks %v, the host holds %s; want one to 10 GiB and 7 GiB held", shrinks, gib(x.Resident))
	}

	// The idle four start a turn: the VM grows on the next sample.
	x.Idle = 0
	if d := p.decide(&st, x, start.Add(601*memTick)); d.Target != 13*GiB || d.Shrink {
		t.Errorf("all busy again: %+v, want a grow to 13 GiB at once", d)
	}
}
