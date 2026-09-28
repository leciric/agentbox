package agent

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Memory thrash: an agent held at its memory limit doesn't die, it slows down
// everyone.
//
// A capped agent is kept out of swap (MemorySwap), so the kernel's only way to
// keep it under memory.max is to drop its page cache — the binaries, libraries
// and files it is running from — and read them back from disk the moment
// they're needed again. That never OOM-kills anything, since file pages can
// always be dropped; it just goes on. organic/agent-03, capped at 4 GiB and
// running kind plus a set of Docker services, kept about 270 MB of file cache,
// refaulted 19 million pages and re-read about 215 GB from the host's SSD in 25
// minutes (roughly 140 MB/s on average, 400 MB/s at its worst). The whole
// desktop froze behind that I/O, and nothing in AgentBox showed which agent it
// was. ThrashWatch is what shows it.
//
// It samples each running agent's cgroup, every ThrashInterval, from four
// files the kernel keeps anyway — no Incus call, no process walk:
//
//   - memory.pressure (PSI): how much of the time the agent's tasks were
//     stalled waiting on memory. PSI counts the stall on refaulting a page
//     that was only just evicted, which is exactly this failure.
//   - memory.stat's workingset_refault_file: pages read back in soon after
//     being dropped. A cgroup whose working set fits refaults close to none,
//     however much it reads, because a first read isn't a refault — which is
//     why the raw read rate, high in any fresh `npm ci` or `git clone`, isn't
//     the signal on its own.
//   - memory.events' max and high: how often the cgroup hit its ceiling. It is
//     what says the stall is this agent's own limit (or the shared budget's),
//     rather than the whole host being short, which raising one agent's limit
//     wouldn't help.
//   - io.stat's rbytes: what it read from disk, shown as the cost to everyone
//     else.
const (
	// ThrashInterval is how often every running agent's cgroup is sampled:
	// often enough to warn within half a minute of it starting, and a handful
	// of small file reads per agent each time.
	ThrashInterval = 5 * time.Second
	// thrashWindow is what the rates are measured over: long enough that one
	// burst (a container image unpacked, a test suite's first run) doesn't
	// count, short enough to say so while it's still happening.
	thrashWindow = 30 * time.Second

	// thrashPressure is the memory pressure, PSI's "some avg60" in percent,
	// that counts as thrashing: for a whole minute, at least one of the
	// agent's tasks spent a fifth of its time stalled on memory. A healthy
	// agent sits at 0; systemd-oomd's default is to kill a unit at 60%, so this
	// warns well before anything that drastic would. PSI's own 60-second
	// average is what makes it "sustained".
	thrashPressure = 20.0
	// thrashRefaults is the refault rate, in bytes a second over thrashWindow,
	// that counts as thrashing: 32 MiB/s is 8,192 pages a second of reading
	// back what was just dropped, about a gigabyte in the window. A healthy
	// agent refaults next to nothing; organic/agent-03 averaged over four
	// times this, for 25 minutes.
	thrashRefaults = 32 << 20

	// A warning clears only once both have fallen well below where it was
	// raised, so an agent hovering at the line doesn't flicker on and off.
	thrashPressureClear = thrashPressure / 2
	thrashRefaultsClear = thrashRefaults / 4
)

// MemoryThrash is an agent short of memory: held at its limit, and re-reading
// from disk what it had to drop to stay under it.
type MemoryThrash struct {
	Instance string
	Since    time.Time // when it was first seen thrashing, this time
	// Pressure is PSI's "some avg60" for its cgroup: the share of the last
	// minute, in percent, that some of its tasks were stalled on memory.
	Pressure float64
	// RefaultRate is how fast it reads back pages it had just dropped, and
	// ReadRate how fast it reads from disk at all, in bytes a second over the
	// last thrashWindow.
	RefaultRate int64
	ReadRate    int64
	// Limit is its own memory ceiling, memory.max, in bytes: 0 when it has
	// none, and it's the shared budget holding it (InBudget).
	Limit    int64
	InBudget bool
	// RaiseTo is the limit to offer it, as limits.memory takes it ("8GiB"),
	// or "" when there's no room to offer more (ThrashWatch.Raise).
	RaiseTo string
}

// cgroupSample is one reading of an agent's cgroup.
type cgroupSample struct {
	at        time.Time
	pressure  float64 // memory.pressure, some avg60
	refaults  int64   // memory.stat workingset_refault_file, in pages
	limitHits int64   // memory.events max + high, the agent's own
	budgetHit int64   // the same, for the shared budget's cgroup when it's in it
	readBytes int64   // io.stat rbytes
	limit     int64   // memory.max in bytes; 0 is "max"
	inBudget  bool
}

type thrashTrack struct {
	samples []cgroupSample // oldest first, thrashWindow and one more
	on      *MemoryThrash
}

// ThrashWatch keeps the recent samples of every running agent's cgroup and
// says which agents are thrashing. The daemon keeps one, and Samples it on a
// ticker whether or not anyone is watching: it's cheap, and `agentbox limits`
// asks for it too.
type ThrashWatch struct {
	// Root is the cgroup root, cgroupRoot outside tests.
	Root string
	// Raise works out the limit to offer an agent that's thrashing, from its
	// instance and current limit. nil offers none.
	Raise func(ctx context.Context, instance string, limit int64) string

	mu     sync.Mutex
	tracks map[string]*thrashTrack
}

// NewThrashWatch watches the agents' cgroups under the host's cgroup root.
func NewThrashWatch(raise func(ctx context.Context, instance string, limit int64) string) *ThrashWatch {
	return &ThrashWatch{Root: cgroupRoot, Raise: raise}
}

// Sample reads every running agent's cgroup once, and returns the agents that
// started or stopped thrashing since the last time.
func (w *ThrashWatch) Sample(ctx context.Context, now time.Time) (started, stopped []MemoryThrash) {
	cgroups := agentCgroups(w.Root)
	w.mu.Lock()
	if w.tracks == nil {
		w.tracks = map[string]*thrashTrack{}
	}
	var raise []*MemoryThrash
	budgetDir := filepath.Join(w.Root, BudgetCgroup)
	budgetHits := int64(-1)
	for instance, dir := range cgroups {
		s, ok := readCgroupSample(dir, now)
		if !ok {
			continue
		}
		if s.inBudget = filepath.Dir(dir) == budgetDir; s.inBudget {
			if budgetHits < 0 {
				budgetHits = limitHits(budgetDir)
			}
			s.budgetHit = budgetHits
		}
		t := w.tracks[instance]
		if t == nil {
			t = &thrashTrack{}
			w.tracks[instance] = t
		}
		if n := len(t.samples); n > 0 && t.samples[n-1].limit != s.limit {
			// Its limit changed — most likely raised from the warning itself.
			// What was measured under the old one says nothing about the new
			// one, so the warning goes and a whole new window has to show it.
			t.samples = nil
			if t.on != nil {
				stopped = append(stopped, *t.on)
				t.on = nil
			}
		}
		t.samples = trimSamples(append(t.samples, s), thrashWindow)
		was := t.on != nil
		next := assessThrash(t.samples, was)
		switch {
		case next != nil && !was:
			next.Instance, next.Since = instance, now
			t.on = next
			started = append(started, *next)
			raise = append(raise, t.on)
		case next != nil:
			next.Instance, next.Since, next.RaiseTo = instance, t.on.Since, t.on.RaiseTo
			t.on = next
		case was:
			stopped = append(stopped, *t.on)
			t.on = nil
		}
	}
	for instance, t := range w.tracks {
		if _, ok := cgroups[instance]; !ok {
			// Stopped, or destroyed: its cgroup is gone, and so is the warning.
			if t.on != nil {
				stopped = append(stopped, *t.on)
			}
			delete(w.tracks, instance)
		}
	}
	w.mu.Unlock()

	// The offer reads settings, so it's worked out outside the lock, and only
	// when a warning starts.
	if w.Raise != nil {
		for _, th := range raise {
			to := w.Raise(ctx, th.Instance, th.Limit)
			w.mu.Lock()
			if t := w.tracks[th.Instance]; t != nil && t.on != nil {
				t.on.RaiseTo = to
			}
			w.mu.Unlock()
			for i := range started {
				if started[i].Instance == th.Instance {
					started[i].RaiseTo = to
				}
			}
		}
	}
	sort.Slice(started, func(i, j int) bool { return started[i].Instance < started[j].Instance })
	sort.Slice(stopped, func(i, j int) bool { return stopped[i].Instance < stopped[j].Instance })
	return started, stopped
}

// Thrashing reports whether an agent's machine is thrashing right now, and
// how badly.
func (w *ThrashWatch) Thrashing(instance string) (MemoryThrash, bool) {
	if w == nil {
		return MemoryThrash{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if t := w.tracks[instance]; t != nil && t.on != nil {
		return *t.on, true
	}
	return MemoryThrash{}, false
}

// assessThrash decides from an agent's recent samples, oldest first, whether
// it is thrashing; was is whether it already was, for the lower bar that
// clears it. It returns nil for "no", and before a whole window has been
// sampled.
func assessThrash(samples []cgroupSample, was bool) *MemoryThrash {
	if len(samples) < 2 {
		return nil
	}
	first, last := samples[0], samples[len(samples)-1]
	span := last.at.Sub(first.at)
	if span < thrashWindow {
		return nil
	}
	secs := span.Seconds()
	rate := func(from, to int64) int64 {
		if to < from {
			return 0 // a counter that went back: the cgroup was made afresh
		}
		return int64(float64(to-from) / secs)
	}
	th := &MemoryThrash{
		Pressure:    last.pressure,
		RefaultRate: rate(first.refaults, last.refaults) * int64(os.Getpagesize()),
		ReadRate:    rate(first.readBytes, last.readBytes),
		Limit:       last.limit,
		InBudget:    last.inBudget,
	}
	if was {
		if th.Pressure < thrashPressureClear && th.RefaultRate < thrashRefaultsClear {
			return nil
		}
		return th
	}
	atLimit := last.limitHits > first.limitHits || (last.inBudget && last.budgetHit > first.budgetHit)
	if !atLimit {
		return nil
	}
	if th.Pressure < thrashPressure && th.RefaultRate < thrashRefaults {
		return nil
	}
	return th
}

// trimSamples drops the samples that are no longer needed: all but the newest
// one at least window older than the last.
func trimSamples(samples []cgroupSample, window time.Duration) []cgroupSample {
	last := samples[len(samples)-1].at
	keep := 0
	for i, s := range samples {
		if last.Sub(s.at) >= window {
			keep = i
		}
	}
	return samples[keep:]
}

// agentCgroups finds every running agent's cgroup directory, by instance: in
// the shared budget (agentbox/ab-*) or at the root on its own
// (lxc.payload.ab-*), the same two places agentCgroup looks.
func agentCgroups(root string) map[string]string {
	out := map[string]string{}
	if dirs, err := filepath.Glob(filepath.Join(root, "lxc.payload.ab-*")); err == nil {
		for _, dir := range dirs {
			out[strings.TrimPrefix(filepath.Base(dir), "lxc.payload.")] = dir
		}
	}
	if dirs, err := filepath.Glob(filepath.Join(root, BudgetCgroup, "ab-*")); err == nil {
		for _, dir := range dirs {
			if strings.HasSuffix(dir, ".monitor") {
				continue // liblxc's monitor process, not the machine
			}
			out[filepath.Base(dir)] = dir
		}
	}
	return out
}

// readCgroupSample reads one sample of a cgroup. ok is false when it can't be
// read at all — it went away in between, or isn't cgroup v2 — and a file
// missing on its own reads as zero: io.stat, for one, is absent where the io
// controller isn't enabled.
func readCgroupSample(dir string, now time.Time) (cgroupSample, bool) {
	s := cgroupSample{at: now}
	pressure, err := os.ReadFile(filepath.Join(dir, "memory.pressure"))
	if err != nil {
		return s, false
	}
	s.pressure = pressureAvg60(string(pressure))
	if stat, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
		s.refaults = statValue(string(stat), "workingset_refault_file")
	}
	s.limitHits = limitHits(dir)
	if io, err := os.ReadFile(filepath.Join(dir, "io.stat")); err == nil {
		s.readBytes = ioReadBytes(string(io))
	}
	if ceiling, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil {
		s.limit, _ = strconv.ParseInt(strings.TrimSpace(string(ceiling)), 10, 64) // "max" is 0
	}
	return s, true
}

// limitHits is how many times a cgroup has hit memory.max or memory.high,
// from memory.events.
func limitHits(dir string) int64 {
	b, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		return 0
	}
	return statValue(string(b), "max") + statValue(string(b), "high")
}

// pressureAvg60 reads "some avg60" out of a PSI file:
//
//	some avg10=0.00 avg60=0.00 avg300=0.00 total=0
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=0
func pressureAvg60(psi string) float64 {
	for _, line := range strings.Split(psi, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(f, "avg60="); ok {
				n, _ := strconv.ParseFloat(v, 64)
				return n
			}
		}
	}
	return 0
}

// statValue reads one "key value" line out of a flat-keyed cgroup file such
// as memory.stat or memory.events.
func statValue(file, key string) int64 {
	for _, line := range strings.Split(file, "\n") {
		k, v, ok := strings.Cut(line, " ")
		if ok && k == key {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// ioReadBytes reads the bytes a cgroup has read from disk out of io.stat,
// one line per device:
//
//	259:4 rbytes=799723520 wbytes=74825728 rios=12583 ...
//	253:0 rbytes=799723520 wbytes=74825728 rios=12583 ...
//
// It takes the largest device's, not the sum: a read through a stacked device
// — LUKS or LVM over an NVMe, as above — is counted once on each layer, and
// an agent's machine reads from the one storage pool it lives on anyway.
func ioReadBytes(stat string) int64 {
	var most int64
	for _, line := range strings.Split(stat, "\n") {
		for _, f := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(f, "rbytes="); ok {
				n, _ := strconv.ParseInt(v, 10, 64)
				most = max(most, n)
			}
		}
	}
	return most
}

// MemoryRaise is the memory limit to offer an agent that's thrashing at limit
// bytes, as limits.memory takes it: twice what it has — organic/agent-03 went
// from 4 GiB to 8 and stopped — in whole GiB, within what it may have. That
// is the shared budget's memory when the agent is in it, since no agent can
// use more than all of it; otherwise the host's memory less what
// SuggestBudget keeps for the host itself (a third, at least 6 GiB). "" is no
// offer: no limit of its own to raise, or no room for a quarter more — less
// than that wouldn't end the thrashing, only move it.
func (m *Manager) MemoryRaise(ctx context.Context, instance string, limit int64) string {
	if limit <= 0 {
		return ""
	}
	var ceiling int64
	if on, b, err := m.SharedBudget(ctx); err == nil && on && InBudget(instance) {
		ceiling = sizeOf(b.Memory)
	} else {
		host := HostMemory()
		ceiling = host - max(host/3, 6<<30)
	}
	return raiseTarget(limit, ceiling)
}

// raiseTarget is MemoryRaise's arithmetic: twice limit, at most ceiling, in
// whole GiB, and "" unless that's at least a quarter more than limit.
func raiseTarget(limit, ceiling int64) string {
	const gib = int64(1) << 30
	target := min(2*limit, ceiling) / gib * gib
	if limit <= 0 || target < limit+limit/4 {
		return ""
	}
	return strconv.FormatInt(target/gib, 10) + "GiB"
}
