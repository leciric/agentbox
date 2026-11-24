package pressure

import (
	"testing"
	"time"
)

func TestParseReadsBothLines(t *testing.T) {
	psi, err := Parse("some avg10=1.36 avg60=0.52 avg300=0.22 total=46356041310\nfull avg10=12.50 avg60=0.50 avg300=0.21 total=40694621386\n")
	if err != nil {
		t.Fatal(err)
	}
	if psi.Some.Avg10 != 1.36 || psi.Full.Avg10 != 12.5 || psi.Full.Total != 40694621386 || psi.Some.Avg300 != 0.22 {
		t.Fatalf("parsed %+v", psi)
	}
	if _, err := Parse("nothing here"); err == nil {
		t.Fatal("no lines parsed without an error")
	}
	if _, err := Parse("some avg10=x"); err == nil {
		t.Fatal("a bad number parsed")
	}
}

func psi(some, full float64) Sample {
	return Sample{PSI: PSI{Some: Stall{Avg10: some}, Full: Stall{Avg10: full}}}
}

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// A run starts the moment it asks, whatever the VM's memory says, and asking
// again is the same run: nothing waits for memory (19 Nov 2026).
func TestRunsStartAtOnceWhateverTheMemory(t *testing.T) {
	g := NewRuns(DefaultPolicy)
	for i, s := range []Sample{psi(80, 60), atCap(0.5, 4), {HostBusy: true}, {}} {
		g.Observe(t0, s)
		key := string(rune('a' + i))
		if r := g.Ask("p/a", key, "go test ./...", time.Minute, t0); r.Started.IsZero() {
			t.Fatalf("%s didn't start under %s: %+v", key, s, r)
		}
	}
	if r := g.Ask("p/a", "a", "", time.Hour, t0.Add(time.Second)); r.Until != t0.Add(time.Hour+time.Second) || len(g.Running()) != 4 {
		t.Errorf("asking again: %+v, %d runs", r, len(g.Running()))
	}
	if !g.Place("p/a", "a") || !g.Get("p/a", "a").Placed || g.Place("p/x", "a") {
		t.Error("Place")
	}
	// A run that isn't placed lapses; a placed one lasts while alive says.
	gone := g.Expire(func(r *Run) bool { return r.Placed })
	if len(gone) != 3 || len(g.Running()) != 1 {
		t.Errorf("expired %v", keys(gone))
	}
}

func TestDropTakesEveryRunOfAnAgent(t *testing.T) {
	g := NewRuns(DefaultPolicy)
	g.Ask("p/a", "1", "", time.Hour, t0)
	g.Ask("p/a", "2", "", time.Hour, t0)
	g.Ask("p/b", "3", "", time.Hour, t0)
	if gone := g.Drop("p/a"); len(gone) != 2 {
		t.Fatalf("dropped %v", keys(gone))
	}
	if len(g.Running()) != 1 {
		t.Fatal("dropped another agent's run")
	}
	if g.End("p/x", "nothing") != nil || g.End("p/b", "3") == nil || len(g.Running()) != 0 {
		t.Fatal("End")
	}
}

func keys(runs []*Run) []string {
	var out []string
	for _, r := range runs {
		out = append(out, r.Key)
	}
	return out
}

// Why says how short the VM's memory is, and holds nothing. The readings of
// 19 Nov 2026: every agent throttled at its shared memory.high, 57–68% of
// the time stalled, with the VM's memory free, is Stalling, and builds still
// start at once.
func TestWhyIsHowShortMemoryIs(t *testing.T) {
	busy := psi(0, 0)
	busy.HostBusy = true
	throttled := Sample{PSI: PSI{Some: Stall{Avg10: 62}, Full: Stall{Avg10: 8}},
		Mem: Mem{Total: gibs(8), Cap: gibs(8), Available: gibs(3), SwapTotal: gibs(2), SwapFree: gibs(1)}}
	for _, c := range []struct {
		name string
		s    Sample
		want Reason
	}{
		{"calm", psi(1, 0), ""},
		{"some stall", psi(DefaultPolicy.Admit, 0), Stalling},
		{"full stall", psi(0, DefaultPolicy.Freeze), Stalling},
		{"under the floor, swap full", atCap(1, 4), Low},
		{"the user's computer busy", busy, HostBusy},
		{"19 Nov 2026, agents at their shared limit", throttled, Stalling},
		{"a full swap of cold pages with memory to spare", trace20261012(0), ""},
	} {
		g := NewRuns(DefaultPolicy)
		g.Observe(t0, c.s)
		if g.Why() != c.want {
			t.Errorf("%s: Why() = %q, want %q", c.name, g.Why(), c.want)
		}
		if r := g.Ask("p/a", "k", "cargo build", time.Minute, t0); r.Started.IsZero() {
			t.Errorf("%s: a build waits", c.name)
		}
	}
}

func atCap(availGiB, swapUsedGiB float64) Sample {
	return Sample{Mem: Mem{Total: gibs(15.9), Cap: gibs(16), Available: gibs(availGiB), SwapTotal: gibs(4), SwapFree: gibs(4 - swapUsedGiB)}}
}

func TestHeadroom(t *testing.T) {
	p := DefaultPolicy
	still := Trend{}
	falling := func(gibPerSec float64) Trend { return Trend{Fall: gibPerSec * (1 << 30)} }
	churning := Trend{Churn: 200 << 20}
	// A 15.9 GiB VM's floor is 1.59 GiB, Low 3.18.
	for _, tc := range []struct {
		name string
		mem  Mem
		tr   Trend
		want Headroom
	}{
		{"short of its cap, nearly nothing available", Mem{Total: gibs(8), Cap: gibs(16), Available: gibs(0.1), SwapTotal: gibs(4)}, falling(1), Room},
		{"at its cap with plenty available", atCap(8, 4).Mem, still, Room},
		{"swap full of cold pages, a third available, still", atCap(5.5, 4).Mem, still, Room},
		{"a third available, swap full, falling slowly", atCap(5.5, 4).Mem, falling(0.01), Room},
		{"a third available, swap full, falling a byte a minute", atCap(5.5, 4).Mem, Trend{Fall: 1.0 / 60}, Room},
		{"a third available, swap busy, falling", atCap(5.5, 2.5).Mem, falling(0.05), Tight},
		{"little available, swap to spare", atCap(2, 1).Mem, still, Room},
		{"little available, swap full, still", atCap(3, 4).Mem, still, Tight},
		{"plenty available, swap full, churning", atCap(6, 4).Mem, churning, Tight},
		{"little available, swap full, churning", atCap(3, 4).Mem, churning, Short},
		{"swap full, falling to the floor in 20s", atCap(5.5, 4).Mem, falling(0.2), Short},
		{"swap full, falling to the floor in 5s", atCap(3, 4).Mem, falling(0.3), Out},
		{"under the floor, swap full", atCap(1, 4).Mem, still, Out},
		{"under the floor, swap to spare", atCap(1, 2).Mem, still, Room},
		{"nearly nothing available, no swap", Mem{Total: gibs(16), Available: gibs(1)}, still, Out},
		{"unread", Mem{}, falling(1), Room},
	} {
		if got := p.Headroom(tc.mem, tc.tr); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestATrendIsMeasuredOverASpan(t *testing.T) {
	g := NewRuns(DefaultPolicy)
	at := func(sec int, availGiB, refaultedGiB float64) Trend {
		s := atCap(availGiB, 4)
		s.Mem.Refaulted = gibs(refaultedGiB)
		g.Observe(t0.Add(time.Duration(sec)*time.Second), s)
		return g.Trend()
	}
	at(0, 8, 0)
	// A gigabyte gone in two seconds isn't a ramp yet: under a Span.
	if tr := at(2, 7, 0); tr.Fall != 0 {
		t.Errorf("fall over 2s = %v, want none yet", tr.Fall)
	}
	if tr := at(10, 7, 1); tr.Fall != 0.1*(1<<30) || tr.Churn != 0.1*(1<<30) {
		t.Errorf("over 10s: %+v, want 0.1 GiB/s of each", tr)
	}
	// Past the Window, the readings before it are forgotten.
	at(20, 7, 1)
	at(30, 7, 1)
	if tr := at(41, 7, 1); tr != (Trend{}) {
		t.Errorf("held still for 30s: %+v, want nothing", tr)
	}
}

// trace20261012 is the VM on nightly .84 from 19:47 to 19:56 on 2026-10-12:
// at its 17.8 GiB cap, 6.2 GiB available, zram 4.38 of 4.5 GiB used by idle
// agents' cold pages, PSI some/full about 0.3/0.2, next to nothing swapped
// back in. #273's rules paused sai-app-4/agent-33's `npx vp test run` and
// held a `python3 - <<'EOF'` edit for six minutes.
func trace20261012(sec int) Sample {
	return Sample{
		PSI: PSI{Some: Stall{Avg10: 0.3}, Full: Stall{Avg10: 0.2}},
		Mem: Mem{Total: gibs(17.8), Cap: gibs(18), Available: gibs(6.2 - 0.05*float64(sec%3)),
			SwapTotal: gibs(4.5), SwapFree: gibs(4.5 - 4.38), Refaulted: gibs(3) + int64(sec)<<20},
	}
}

func gibs(n float64) int64 { return int64(n * (1 << 30)) }
