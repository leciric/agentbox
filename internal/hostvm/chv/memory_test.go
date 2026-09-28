package chv

import (
	"testing"
	"time"
)

func sample(used, total int64, agents int, pressure float64) *memSample {
	return &memSample{Total: total, Available: total - used, Agents: agents, Pressure: pressure}
}

func TestMemPolicyTarget(t *testing.T) {
	p := memPolicy{Min: 4 * GiB, Cap: 16 * GiB}
	for _, tc := range []struct {
		name   string
		used   int64
		agents int
		want   int64
	}{
		{"idle stays at the minimum", GiB / 2, 0, 4 * GiB},
		{"two GiB of headroom at least", 3 * GiB, 1, 5 * GiB},
		{"a GiB of headroom an agent", 3 * GiB, 4, 7 * GiB},
		{"rounded up to 512 MiB", 3*GiB + 1, 0, 5*GiB + 512<<20},
		{"never above the cap", 20 * GiB, 10, 16 * GiB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.target(*sample(tc.used, 16*GiB, tc.agents, 0)); got != tc.want {
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
	// The numbers say 8 GiB is plenty, but the guest stalls on memory.
	d := p.decide(&st, sample(2*GiB, 8*GiB, 0, 25), now)
	if d.Target != 10*GiB {
		t.Fatalf("decide = %+v, want a grow to 10 GiB", d)
	}
	// Not again until the grow has had time to help.
	if d := p.decide(&st, sample(2*GiB, 10*GiB, 0, 25), now.Add(memTick)); d.Target != 0 {
		t.Errorf("within the cooldown: %+v, want nothing", d)
	}
	if d := p.decide(&st, sample(2*GiB, 10*GiB, 0, 25), now.Add(memGrowCooldown)); d.Target != 12*GiB {
		t.Errorf("after the cooldown: %+v, want 12 GiB", d)
	}
	st.Requested = 16 * GiB
	if d := p.decide(&st, sample(2*GiB, 16*GiB, 0, 50), now.Add(time.Minute)); d.Target != 0 {
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
some avg10=12.50 avg60=3.00 avg300=1.00 total=123
full avg10=1.00 avg60=0.00 avg300=0.00 total=12
auto_online online_movable
agents 3
`
	s, err := parseMemSample(out)
	if err != nil {
		t.Fatal(err)
	}
	want := memSample{Total: 8024064 << 10, Available: 7000000 << 10, Pressure: 12.5, Agents: 3, AutoOnline: "online_movable"}
	if s != want {
		t.Errorf("parseMemSample = %+v, want %+v", s, want)
	}
	if _, err := parseMemSample("sh: 1: cat: not found\n"); err == nil {
		t.Error("parsed a sample out of nothing")
	}
}
