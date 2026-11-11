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
			if got := p.target(*x, 0, 0); got != tc.want {
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
workingset_refault_file 123456
some avg10=12.50 avg60=3.00 avg300=1.00 total=123
full avg10=1.00 avg60=0.00 avg300=0.00 total=12
disk_swap_used 2048
auto_online online_movable
agents 3
`
	s, err := parseMemSample(out)
	if err != nil {
		t.Fatal(err)
	}
	want := memSample{Total: 8024064 << 10, Available: 7000000 << 10, SwapTotal: 8388604 << 10, SwapFree: 8000000 << 10, DiskSwapUsed: 2048 << 10, Pressure: 12.5, Agents: 3, AutoOnline: "online_movable",
		FileCache: 1500000 << 10, InactiveFile: 500000 << 10, Refaults: 123456, RefaultsKnown: true}
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

	// An agent that runs but idles (waiting on its model): its CPUs are
	// quiet for memIdleAfter, once the smoothed use has come down (about
	// 75 s), but that drops its caches only when the host is short; it would
	// read them all again on its next turn.
	for _, short := range []bool{false, true} {
		st = memState{Requested: 4 * GiB}
		reclaims = nil
		for i := range 600 { // 300 s
			x := s(1, 3*GiB, 1, i)
			if short {
				x.HostTotal, x.HostAvailable = 32*GiB, 2*GiB
			}
			if d := p.decide(&st, x, start.Add(time.Duration(i)*memTick)); d.Reclaim {
				reclaims = append(reclaims, i)
			}
		}
		if want := map[bool]int{false: 0, true: 1}[short]; len(reclaims) != want {
			t.Errorf("idle agent, host short %v: reclaimed at samples %v, want %d", short, reclaims, want)
		}
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
// a time, only when the host is short of memory, and never below its floor;
// with the host fine, never, however much it caches.
func TestMemPolicyTrimsWhileBusy(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	busy := func(cache, inactive int64, hostAvail int64, hostPSI float64) *memSample {
		x := sample(8*GiB, 16*GiB, 3, 0)
		x.FileCache, x.InactiveFile = cache, inactive
		x.Resident = 15 * GiB
		x.HostTotal, x.HostAvailable, x.HostPressure = 32*GiB, hostAvail, hostPSI
		return x
	}
	run := func(s *memSample, ticks int) (trims []memDecision) {
		// At what it should have: use, the cache it keeps, 3 GiB of headroom.
		var st memState
		st.Requested = p.target(*s, 0, st.keep(*s, p))
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

	// The host is stalling: up to memTrimMax every memTrimEvery. 7 GiB
	// cached, 2 of it active, beside 8 in use: the floor is 2 GiB.
	trims := run(busy(7*GiB, 5*GiB, 8*GiB, 25), 240) // 120 s
	if len(trims) != int(120*time.Second/memTrimEvery) || trims[0].Trim != memTrimMax || !trims[0].HostShort || !strings.Contains(trims[0].Reason, "stalled") {
		t.Errorf("host stalling: %+v", trims)
	}
	// The host has little available: what's beyond the floor, 3 GiB less
	// 2 of the allowance, though more is inactive.
	trims = run(busy(3*GiB, 2*GiB+GiB/2, 2*GiB, 0), 2)
	if len(trims) != 1 || trims[0].Trim != GiB || !trims[0].HostShort || !strings.Contains(trims[0].Reason, "keeping 2.0 GiB") {
		t.Errorf("host short: %+v", trims)
	}
	// None of these: the host fine however big the cache (the trim on a
	// timer that read the host's disk all day), the cache within its floor,
	// all of it active, or the host unknown.
	for name, s := range map[string]*memSample{
		"host fine, big cache": busy(7*GiB, 5*GiB, 20*GiB, 0),
		"within the floor":     busy(2*GiB, GiB, 2*GiB, 30),
		"all active":           busy(7*GiB, 100<<20, 2*GiB, 30),
		"host unknown":         busy(7*GiB, 5*GiB, 0, 0),
	} {
		if s.HostAvailable == 0 {
			s.HostTotal = 0
		}
		if trims := run(s, 2400); len(trims) > 0 {
			t.Errorf("%s: trimmed %+v", name, trims)
		}
	}
}

// The numbers the freezes were measured with: the guest at 7 GiB, 5 in use
// and 2 cached, the host fine. Before, the cache beyond 1.25 GiB was trimmed
// every 2 minutes and the target, 5 + 2 GiB of headroom, left the cache no
// room but the headroom: the guest refaulted ~400 MiB a minute, all read from
// the host's NVMe. Now the VM grows to keep its cache and nothing is trimmed.
func TestMemPolicyKeepsTheWorkingSet(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	x := sample(5*GiB, 7*GiB, 2, 0)
	x.FileCache, x.InactiveFile = 2*GiB, GiB
	x.Resident = 7 * GiB
	x.HostTotal, x.HostAvailable = 64*GiB, 30*GiB
	x.RefaultsKnown = true
	st := memState{Requested: 7 * GiB}
	var grew int64
	for i := range 7200 { // an hour
		x.CPUTotal, x.CPUBusy = uint64(i)*100, uint64(i)*60
		d := p.decide(&st, x, start.Add(time.Duration(i)*memTick))
		if d.Trim > 0 || d.Reclaim || d.Shrink {
			t.Fatalf("sample %d: %+v, want nothing taken from the cache", i, d)
		}
		if d.Target > 0 {
			grew = d.Target
			x.Available += d.Target - x.Total
			x.Total = d.Target
		}
	}
	// 5 in use, 2 cached (its floor is 1.25), and 2 of headroom.
	if grew != 8*GiB+GiB/2 {
		t.Errorf("grew to %s, want 8.5 GiB", gib(grew))
	}
}

// Refaults raise the floor to the cache plus a minute of what's read again,
// and hold trims off, doubling; calm, the floor comes back down by eighths.
func TestMemPolicyRefaultsRaiseTheFloor(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	x := sample(8*GiB, 16*GiB, 3, 0)
	x.FileCache, x.InactiveFile = 4*GiB, 3*GiB
	x.Resident = 14 * GiB
	x.HostTotal, x.HostAvailable = 32*GiB, 5*GiB // short: keeps 6.4
	x.RefaultsKnown = true
	st := memState{Requested: 16 * GiB}
	at := func(i int) time.Time { return start.Add(time.Duration(i) * memTick) }
	// 6 MiB/s of refaults, the rate measured: 768 pages a tick.
	const perTick = 6 << 20 / pageSize / 2
	var trims int
	i := 0
	for ; i < 120; i++ { // a minute
		x.Refaults += perTick
		if d := p.decide(&st, x, at(i)); d.Trim > 0 {
			trims++
		}
	}
	// The first trim goes before the rate has built up; then none.
	if trims > 1 {
		t.Errorf("trimmed %d times while refaulting", trims)
	}
	if st.floor < 4*GiB+200<<20 || st.trimBackoff < memTrimBackoff {
		t.Fatalf("floor %s, backoff %s: want above the 4 GiB cached, and trims held off", gib(st.floor), st.trimBackoff)
	}
	// The VM keeps the floor: its target includes it.
	if keep := st.keep(*x, p); keep != x.FileCache {
		t.Errorf("keeps %s of %s cached", gib(keep), gib(x.FileCache))
	}
	// Still refaulting after the first backoff: it doubles.
	first := st.trimBackoff
	for ; i < 120+int(memTrimBackoff/memTick); i++ {
		x.Refaults += perTick
		p.decide(&st, x, at(i))
	}
	if st.trimBackoff <= first {
		t.Errorf("backoff %s after %s of refaults, want more than %s", st.trimBackoff, memTrimBackoff, first)
	}
	// Calm: the backoff resets and the floor comes down by an eighth each
	// memFloorCalm, but not below what's active.
	floor := st.floor
	for end := i + int(3*memFloorCalm/memTick); i < end; i++ {
		p.decide(&st, x, at(i))
	}
	if st.trimBackoff != 0 || st.floor >= floor || st.floor < floor/2 {
		t.Errorf("calm: floor %s from %s, backoff %s", gib(st.floor), gib(floor), st.trimBackoff)
	}
	// Once the floor is back under the cache, and the backoff is over, the
	// host still short: trims again, down to the floor.
	st.floor = 0
	st.noTrimUntil = time.Time{}
	d := p.decide(&st, x, at(i+int(memTrimEvery/memTick)))
	if d.Trim != 2*GiB || !d.HostShort {
		t.Errorf("after the calm: %+v, want 2 GiB trimmed (4 cached, 2 kept)", d)
	}
}

// A shrink trims only the cache beyond the floor, never all of it (it
// dropped all of it before: the VM shrank from 10 to 8 GiB and read its disk
// at 44 to 73 MB/s straight after).
func TestMemPolicyShrinkKeepsTheFloor(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	x := sample(4*GiB, 12*GiB, 1, 0)
	x.FileCache, x.InactiveFile = 5*GiB, 4*GiB // 1 active: the floor is the 1 GiB allowance
	x.Resident = 9 * GiB
	st := memState{Requested: 12 * GiB}
	var d memDecision
	for i := 0; d.Target == 0 && i < 1000; i++ {
		d = p.decide(&st, x, start.Add(time.Duration(i)*memTick))
	}
	// 4 in use, 1 of cache kept, 2 of headroom.
	if !d.Shrink || d.Target != 7*GiB || d.Reclaim {
		t.Fatalf("decide = %+v, want a shrink to 7 GiB without dropping every cache", d)
	}
	if d.Trim != 4*GiB {
		t.Errorf("trims %s with the shrink, want the 4 GiB beyond the floor", gib(d.Trim))
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
	got := trimScript(2 * GiB)
	for _, want := range []string{
		"pgrep -f '^tee /sys/fs/cgroup/.*memory[.]reclaim' >/dev/null ||", // one writer at a time
		"echo 2147483648 | sudo -n timeout --foreground -s KILL 10 tee /sys/fs/cgroup/memory.reclaim",
		"compact_memory",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trimScript = %q, want %q in it", got, want)
		}
	}
	if strings.Contains(got, "swappiness") {
		t.Errorf("trimScript = %q, still forces a swappiness", got)
	}
}

// The numbers from a real host (Linux, nightly .134): the VM plugged to
// 13.5 GiB while its programs use 7 (anon 6.3, kernel 0.5), six agents
// running, 4.5 GiB of page cache, 1.5 of it active, and the host at 67% used,
// so not short (it was 73% on that host, which now keeps a quarter). The cache is the VM's to keep while the host isn't short:
// nothing is trimmed, and idle agents keep little headroom; growth stays
// instant once they're busy again.
func TestMemPolicyRealHostHeadroom(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	host := func(idle int) *memSample {
		x := sample(7*GiB, 13*GiB+GiB/2, 6, 0)
		x.Idle = idle
		x.FileCache, x.InactiveFile = 4*GiB+GiB/2, 3*GiB
		x.Resident = 13 * GiB
		x.HostTotal, x.HostAvailable = 32*GiB, 32*GiB*33/100
		return x
	}
	// replay runs the policy for ticks samples, with the guest doing what it's
	// told: a trim drops that much inactive cache; a shrink unplugs.
	replay := func(x *memSample, st *memState, ticks int) (trims []int64, shrinks []int64) {
		for i := range ticks {
			x.CPUTotal, x.CPUBusy = uint64(i)*100, uint64(i)*60
			d := p.decide(st, x, start.Add(time.Duration(i)*memTick))
			if d.Trim > 0 {
				trims = append(trims, d.Trim)
				x.FileCache -= d.Trim
				x.InactiveFile -= d.Trim
			}
			if d.Target > 0 {
				x.Available += d.Target - x.Total
				x.Total = d.Target
			}
			if d.Shrink {
				shrinks = append(shrinks, d.Target)
			}
		}
		return trims, shrinks
	}

	// All six busy: 7 in use, the 1.75 GiB floor of cache, 6 of headroom:
	// it grows to 15 GiB, and trims nothing.
	x, st := host(0), memState{Requested: 13*GiB + GiB/2}
	trims, shrinks := replay(x, &st, 600) // 5 min
	if len(trims) > 0 || len(shrinks) > 0 || x.Total != 15*GiB {
		t.Errorf("all busy: trims %v, shrinks %v, at %s; want none, at 15 GiB", trims, shrinks, gib(x.Total))
	}

	// Two busy and four idle: 3 GiB of headroom, so the VM shrinks to 12 GiB
	// once that's lasted memShrinkAfter, trimming the cache beyond its floor
	// as far as the shrink needs.
	x, st = host(4), memState{Requested: 13*GiB + GiB/2}
	trims, shrinks = replay(x, &st, 600)
	if !slices.Equal(shrinks, []int64{12 * GiB}) || !slices.Equal(trims, []int64{GiB + GiB/2}) {
		t.Errorf("four idle: shrinks %v, trims %v; want one to 12 GiB trimming 1.5", shrinks, trims)
	}

	// The idle four start a turn: the VM grows on the next sample.
	x.Idle = 0
	if d := p.decide(&st, x, start.Add(601*memTick)); d.Target != 15*GiB || d.Shrink {
		t.Errorf("all busy again: %+v, want a grow to 15 GiB at once", d)
	}
}

// The host keeps the larger of 4 GiB and a quarter of its memory, and a cap
// leaves it that, in whole GiB, never under what the VM boots with.
func TestHostReserve(t *testing.T) {
	for _, tc := range []struct{ total, reserve, most int64 }{
		{8 * GiB, 4 * GiB, 4 * GiB},
		{6 * GiB, 4 * GiB, DefaultMemoryMin}, // never below what it boots with
		{16 * GiB, 4 * GiB, 12 * GiB},
		{20 * GiB, 5 * GiB, 15 * GiB}, // a quarter
		{24 * GiB, 6 * GiB, 18 * GiB},
		{32 * GiB, 8 * GiB, 24 * GiB},
		{64 * GiB, 16 * GiB, 48 * GiB},
		{0, 4 * GiB, 0}, // unknown: no limit
	} {
		if got := HostReserve(tc.total); got != tc.reserve {
			t.Errorf("HostReserve(%s) = %s, want %s", gib(tc.total), gib(got), gib(tc.reserve))
		}
		if got := MaxCap(tc.total); got != tc.most {
			t.Errorf("MaxCap(%s) = %s, want %s", gib(tc.total), gib(got), gib(tc.most))
		}
	}
}

// A cap that leaves the host less than its reserve is lowered to MaxCap, a
// Mac's too; one within it and one on a host that can't be read are left
// alone.
func TestFitHost(t *testing.T) {
	// 24 GiB keeps 6: 18 is the most. The 17.8 GiB cap that left a desktop
	// struggling fits: what keeps the host its reserve then is the growth
	// gate (TestMemPolicyHostGate).
	c := Config{MemoryMin: 4 * GiB, MemoryCap: 17*GiB + 800<<20}
	if got, lowered := FitHost(c, 24*GiB); lowered || got != c {
		t.Errorf("17.8 GiB on 24: lowered to %s", gib(got.MemoryCap))
	}
	c.MemoryCap = 22 * GiB
	if got, lowered := FitHost(c, 24*GiB); !lowered || got.MemoryCap != 18*GiB {
		t.Errorf("22 GiB on 24: %s, lowered %v; want 18 GiB", gib(got.MemoryCap), lowered)
	}
	mac := Config{MemoryMin: 4 * GiB, MemoryCap: 24 * GiB, Driver: DriverVZ}
	if got, lowered := FitHost(mac, 24*GiB); !lowered || got.MemoryCap != 18*GiB {
		t.Errorf("a Mac's 24 GiB on 24: %s, lowered %v; want 18 GiB", gib(got.MemoryCap), lowered)
	}
	for name, tc := range map[string]struct {
		c     Config
		total int64
	}{
		"within it": {Config{MemoryMin: 4 * GiB, MemoryCap: 16 * GiB}, 24 * GiB},
		"unknown":   {Config{MemoryMin: 4 * GiB, MemoryCap: 24 * GiB}, 0},
	} {
		if got, lowered := FitHost(tc.c, tc.total); lowered || got != tc.c {
			t.Errorf("%s: lowered to %s", name, gib(got.MemoryCap))
		}
	}
}

// Growing stops where the host would be left less than its reserve,
// whatever the agents want or the guest's stalls say; with the host
// unknown it grows as before.
func TestMemPolicyHostGate(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	now := time.Now()
	// 32 GiB keeps 8. The VM holds 8, and 8 agents want 3+8 = 11.
	host := func(avail int64) *memSample {
		x := sample(3*GiB, 8*GiB, 8, 0)
		x.Resident = 8 * GiB
		x.HostTotal, x.HostAvailable = 32*GiB, avail
		return x
	}
	for _, tc := range []struct {
		name  string
		avail int64
		want  int64 // 0: no grow
	}{
		{"plenty", 20 * GiB, 11 * GiB},
		{"room for 1.5 GiB more", 9*GiB + 512<<20, 9*GiB + 512<<20},
		{"at the reserve", 8*GiB + 400<<20, 0},
		{"below it", 3 * GiB, 0},
	} {
		st := memState{Requested: 8 * GiB}
		d := p.decide(&st, host(tc.avail), now)
		if d.Target != tc.want || d.Shrink {
			t.Errorf("%s: %+v, want a target of %s", tc.name, d, gib(tc.want))
		}
		if tc.want != 0 && tc.want != 11*GiB && !strings.Contains(d.Reason, "keeps 8.0 GiB for itself") {
			t.Errorf("%s: reason %q", tc.name, d.Reason)
		}
	}
	// What's plugged stands in for what's held when that's unknown.
	x := host(9*GiB + 512<<20)
	x.Resident = 0
	if d := p.decide(&memState{Requested: 8 * GiB}, x, now); d.Target != 9*GiB+512<<20 {
		t.Errorf("held unknown: %+v", d)
	}
	// A stall doesn't get past it.
	x = host(6 * GiB)
	x.Available, x.Pressure = GiB, 50
	x.Total = 8 * GiB
	if d := p.decide(&memState{Requested: 8 * GiB}, x, now); d.Target != 0 {
		t.Errorf("stalled, host below its reserve: %+v, want no grow", d)
	}
	// The host unknown: no gate.
	x = host(0)
	x.HostTotal = 0
	if d := p.decide(&memState{Requested: 8 * GiB}, x, now); d.Target != 11*GiB {
		t.Errorf("host unknown: %+v", d)
	}
}

// With the host below its reserve, the VM gives back within
// memShrinkAfterHost what's beyond use and memHeadroomMin, rather than
// keeping a busy agent's headroom for memShrinkAfter.
func TestMemPolicyGivesBackToTheHost(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	start := time.Now()
	// Six busy agents, 7 GiB in use, 13 plugged: 7+6 is just what they keep.
	x := sample(7*GiB, 13*GiB, 6, 0)
	x.Resident = 13 * GiB
	x.HostTotal, x.HostAvailable = 32*GiB, 5*GiB // keeps 8
	st := memState{Requested: 13 * GiB}
	var got memDecision
	var at time.Duration
	for i := range 40 {
		if d := p.decide(&st, x, start.Add(time.Duration(i)*memTick)); d.Target != 0 {
			got, at = d, time.Duration(i)*memTick
			break
		}
	}
	if !got.Shrink || got.Target != 9*GiB || at != memShrinkAfterHost {
		t.Fatalf("host short: %+v after %s, want a shrink to 9 GiB after %s", got, at, memShrinkAfterHost)
	}
	if !strings.Contains(got.Reason, "5.0 GiB available of the 8.0 GiB it keeps") {
		t.Errorf("reason %q", got.Reason)
	}
	// With the host fine, the same VM keeps its headroom.
	x.HostAvailable = 20 * GiB
	st = memState{Requested: 13 * GiB}
	for i := range 240 {
		if d := p.decide(&st, x, start.Add(time.Duration(i)*memTick)); d.Target != 0 {
			t.Fatalf("host fine: %+v", d)
		}
	}
}

// While the host itself stalls on memory, the VM doesn't grow, even with
// plenty available on the host and its own agents asking, and gives back
// within memShrinkAfterHost.
func TestMemPolicyHostStallStopsGrowth(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 24 * GiB}
	now := time.Now()
	x := sample(3*GiB, 8*GiB, 8, 0)
	x.Resident = 8 * GiB
	x.HostTotal, x.HostAvailable, x.HostPressure = 32*GiB, 20*GiB, 25
	st := memState{Requested: 8 * GiB}
	if d := p.decide(&st, x, now); d.Target != 0 {
		t.Fatalf("host stalling: %+v, want no grow", d)
	}
	// Calm again: it grows as it would have.
	x.HostPressure = 0
	if d := p.decide(&st, x, now); d.Target != 11*GiB {
		t.Fatalf("host calm: %+v, want 11 GiB", d)
	}
	// Stalling with more than it needs: it gives back soon.
	st = memState{Requested: 16 * GiB}
	x = sample(GiB, 16*GiB, 0, 0)
	x.Resident = 16 * GiB
	x.HostTotal, x.HostAvailable, x.HostPressure = 32*GiB, 12*GiB, 25
	p.decide(&st, x, now)
	d := p.decide(&st, x, now.Add(memShrinkAfterHost))
	if !d.Shrink || d.Target >= 16*GiB || !strings.Contains(d.Reason, "stalled on memory") {
		t.Fatalf("host stalling, VM holding more than it needs: %+v", d)
	}
}
