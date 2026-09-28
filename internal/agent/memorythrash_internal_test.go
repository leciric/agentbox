package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCgroup is one agent's cgroup directory in a fake tree, written the way
// the kernel lays the files out.
type fakeCgroup struct {
	dir       string
	pressure  float64
	refaults  int64 // pages
	hits      int64 // memory.events max
	readBytes int64
	limit     string // memory.max
}

func (c *fakeCgroup) write(t *testing.T) {
	t.Helper()
	files := map[string]string{
		"memory.pressure": fmt.Sprintf("some avg10=0.00 avg60=%.2f avg300=0.00 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n", c.pressure),
		"memory.stat":     fmt.Sprintf("anon 1\nfile 2\nworkingset_refault_anon 5\nworkingset_refault_file %d\nworkingset_activate_file 3\n", c.refaults),
		"memory.events":   fmt.Sprintf("low 0\nhigh 0\nmax %d\noom 0\noom_kill 0\noom_group_kill 0\n", c.hits),
		// A LUKS volume over an NVMe: every read counted on both.
		"io.stat":    fmt.Sprintf("259:4 rbytes=%d wbytes=0 rios=1 wios=0 dbytes=0 dios=0\n253:0 rbytes=%d wbytes=0 rios=1 wios=0 dbytes=0 dios=0\n", c.readBytes, c.readBytes),
		"memory.max": c.limit + "\n",
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(c.dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const fourGiB = 4 << 30

// thrashing is organic/agent-03's cgroup, one ThrashInterval at a time: held
// at 4 GiB, hitting it constantly, and re-reading its files at about 80 MB/s.
func thrashing(c *fakeCgroup) {
	c.pressure = 45
	c.refaults += 100_000
	c.hits += 5_000
	c.readBytes += 100_000 * 4096
}

func newTestWatch(root string) *ThrashWatch {
	return &ThrashWatch{Root: root, Raise: func(_ context.Context, _ string, limit int64) string {
		return raiseTarget(limit, 20<<30)
	}}
}

func TestThrashWatchFlagsAnAgentThrashingAtItsLimit(t *testing.T) {
	root := t.TempDir()
	c := &fakeCgroup{dir: filepath.Join(root, "lxc.payload.ab-organic-agent-03"), limit: fmt.Sprint(fourGiB)}
	w := newTestWatch(root)
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()

	var got []MemoryThrash
	at := start
	for i := 0; ; i++ {
		c.write(t)
		started, stopped := w.Sample(ctx, at)
		if len(stopped) != 0 {
			t.Fatalf("stopped %v before it started", stopped)
		}
		if len(started) != 0 {
			got = started
			break
		}
		if i > 10 {
			t.Fatal("never flagged")
		}
		thrashing(c)
		at = at.Add(ThrashInterval)
	}
	// Not before a whole window has been measured.
	if elapsed := at.Sub(start); elapsed != thrashWindow {
		t.Errorf("flagged after %v, want %v", elapsed, thrashWindow)
	}
	th := got[0]
	if th.Instance != "ab-organic-agent-03" || th.Since != at || th.Limit != fourGiB || th.InBudget {
		t.Errorf("thrash = %+v", th)
	}
	if th.RaiseTo != "8GiB" {
		t.Errorf("RaiseTo = %q, want 8GiB", th.RaiseTo)
	}
	wantRefaults := int64(100_000/5) * int64(os.Getpagesize())
	if th.RefaultRate != wantRefaults {
		t.Errorf("RefaultRate = %d, want %d", th.RefaultRate, wantRefaults)
	}
	// Counted once, not once per layer of the LUKS stack.
	if th.ReadRate != 100_000*4096/5 {
		t.Errorf("ReadRate = %d, want %d", th.ReadRate, 100_000*4096/5)
	}
	if now, ok := w.Thrashing("ab-organic-agent-03"); !ok || now.RaiseTo != "8GiB" {
		t.Errorf("Thrashing = %+v, %v", now, ok)
	}

	// Still thrashing: the same warning, from when it started.
	thrashing(c)
	c.write(t)
	at = at.Add(ThrashInterval)
	if started, stopped := w.Sample(ctx, at); len(started)+len(stopped) != 0 {
		t.Errorf("still thrashing, but started %v stopped %v", started, stopped)
	}
	if now, _ := w.Thrashing("ab-organic-agent-03"); now.Since != th.Since || now.RaiseTo != "8GiB" {
		t.Errorf("a warning still on = %+v", now)
	}

	// It settles: the pressure drops, the refaults stop, and once the window
	// has none left in it the warning goes.
	c.pressure = 2
	var cleared bool
	for i := 0; i < 10 && !cleared; i++ {
		at = at.Add(ThrashInterval)
		c.write(t)
		_, stopped := w.Sample(ctx, at)
		cleared = len(stopped) == 1
	}
	if !cleared {
		t.Fatal("never cleared")
	}
	if _, ok := w.Thrashing("ab-organic-agent-03"); ok {
		t.Error("still flagged after clearing")
	}
}

func TestThrashWatchLeavesHealthyAgentsAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		step func(c *fakeCgroup)
	}{
		// A cold `npm ci`: a lot read from disk, all of it for the first time.
		{"reading a lot", func(c *fakeCgroup) { c.readBytes += 2 << 30 }},
		// The host is short, not the agent: it's stalled, and refaulting, but
		// it's nowhere near its own limit, so raising that wouldn't help.
		{"not at its limit", func(c *fakeCgroup) { c.pressure = 50; c.refaults += 100_000 }},
		// At its limit, now and then, and reclaiming without re-reading.
		{"at its limit, calmly", func(c *fakeCgroup) { c.hits++; c.pressure = 3; c.refaults += 100 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			c := &fakeCgroup{dir: filepath.Join(root, "lxc.payload.ab-p-a1"), limit: fmt.Sprint(fourGiB)}
			w := newTestWatch(root)
			at := time.Now()
			for i := 0; i < 20; i++ {
				c.write(t)
				if started, _ := w.Sample(context.Background(), at); len(started) != 0 {
					t.Fatalf("flagged %+v", started[0])
				}
				tc.step(c)
				at = at.Add(ThrashInterval)
			}
		})
	}
}

func TestThrashWatchSharedBudget(t *testing.T) {
	root := t.TempDir()
	budget := &fakeCgroup{dir: filepath.Join(root, BudgetCgroup), limit: fmt.Sprint(20 << 30)}
	// The agent has no limit of its own: the budget is what holds it, and the
	// budget's memory.events is what says so.
	c := &fakeCgroup{dir: filepath.Join(root, BudgetCgroup, "ab-p-a1"), limit: "max"}
	monitor := &fakeCgroup{dir: filepath.Join(root, BudgetCgroup, "ab-p-a1.monitor"), limit: "max"}
	monitor.write(t)
	w := newTestWatch(root)
	at := time.Now()
	var got []MemoryThrash
	for i := 0; i < 10 && got == nil; i++ {
		budget.write(t)
		c.write(t)
		got, _ = w.Sample(context.Background(), at)
		budget.hits += 100
		c.pressure = 30
		at = at.Add(ThrashInterval)
	}
	if len(got) != 1 {
		t.Fatalf("started = %+v, want the one agent (and not its monitor)", got)
	}
	if th := got[0]; th.Instance != "ab-p-a1" || !th.InBudget || th.Limit != 0 || th.RaiseTo != "" {
		t.Errorf("thrash = %+v", th)
	}
}

func TestThrashWatchStartsAgainWhenTheLimitChanges(t *testing.T) {
	root := t.TempDir()
	c := &fakeCgroup{dir: filepath.Join(root, "lxc.payload.ab-p-a1"), limit: fmt.Sprint(fourGiB)}
	w := newTestWatch(root)
	at := time.Now()
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		c.write(t)
		w.Sample(ctx, at)
		thrashing(c)
		at = at.Add(ThrashInterval)
	}
	if _, ok := w.Thrashing("ab-p-a1"); !ok {
		t.Fatal("not flagged")
	}
	// Raised to 8 GiB from the warning. PSI's minute-long average is still
	// high, but a new limit gets a new window.
	c.limit = fmt.Sprint(8 << 30)
	c.write(t)
	if _, stopped := w.Sample(ctx, at); len(stopped) != 1 {
		t.Errorf("stopped = %v, want the warning gone", stopped)
	}
	if _, ok := w.Thrashing("ab-p-a1"); ok {
		t.Error("still flagged after the raise")
	}
}

func TestThrashWatchForgetsAStoppedAgent(t *testing.T) {
	root := t.TempDir()
	c := &fakeCgroup{dir: filepath.Join(root, "lxc.payload.ab-p-a1"), limit: fmt.Sprint(fourGiB)}
	w := newTestWatch(root)
	at := time.Now()
	for i := 0; i < 7; i++ {
		c.write(t)
		w.Sample(context.Background(), at)
		thrashing(c)
		at = at.Add(ThrashInterval)
	}
	if err := os.RemoveAll(c.dir); err != nil {
		t.Fatal(err)
	}
	if _, stopped := w.Sample(context.Background(), at); len(stopped) != 1 || stopped[0].Instance != "ab-p-a1" {
		t.Errorf("stopped = %v", stopped)
	}
	if _, ok := w.Thrashing("ab-p-a1"); ok {
		t.Error("a stopped agent is still flagged")
	}
	var nilWatch *ThrashWatch
	if _, ok := nilWatch.Thrashing("ab-p-a1"); ok {
		t.Error("a nil watch flags something")
	}
}

func TestRaiseTarget(t *testing.T) {
	const gib = int64(1) << 30
	for _, tc := range []struct {
		limit, ceiling int64
		want           string
	}{
		{4 * gib, 20 * gib, "8GiB"},
		{4 * gib, 6 * gib, "6GiB"},        // held to the ceiling
		{4 * gib, 4*gib + gib/2, ""},      // not a quarter more to give
		{8 * gib, 4 * gib, ""},            // already past it
		{3*gib + gib/2, 64 * gib, "7GiB"}, // whole GiB
		{512 << 20, 64 * gib, "1GiB"},     // a small agent
		{7 * gib, 8 * gib, ""},            // a GiB, but not enough to matter
	} {
		if got := raiseTarget(tc.limit, tc.ceiling); got != tc.want {
			t.Errorf("raiseTarget(%d, %d) = %q, want %q", tc.limit, tc.ceiling, got, tc.want)
		}
	}
	if got := (&Manager{}).MemoryRaise(context.Background(), "ab-p-a1", 0); got != "" {
		t.Errorf("MemoryRaise with no limit = %q, want no offer", got)
	}
}

func TestCgroupFileParsers(t *testing.T) {
	if got := pressureAvg60("some avg10=1.00 avg60=23.50 avg300=4.00 total=9\nfull avg10=0.00 avg60=11.00 avg300=0.00 total=0\n"); got != 23.5 {
		t.Errorf("pressureAvg60 = %v, want the some line's 23.5", got)
	}
	if got := pressureAvg60(""); got != 0 {
		t.Errorf("pressureAvg60(empty) = %v", got)
	}
	if got := ioReadBytes("8:0 rbytes=10 wbytes=0\n8:16 rbytes=30 wbytes=5\n"); got != 30 {
		t.Errorf("ioReadBytes = %d, want the busiest device's 30", got)
	}
	if got := statValue("low 0\nhigh 7\nmax 12\n", "max"); got != 12 {
		t.Errorf("statValue(max) = %d", got)
	}
	if _, ok := readCgroupSample(filepath.Join(t.TempDir(), "gone"), time.Now()); ok {
		t.Error("read a sample from a cgroup that isn't there")
	}
}
