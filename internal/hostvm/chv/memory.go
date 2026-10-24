package chv

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The VM's memory is sized as it goes (virtio-mem, vm.resize): it boots with
// Config.MemoryMin, is given more as its agents need it, up to
// Config.MemoryCap, and gives it back once they've stopped needing it.
//
// Every few seconds the supervisor reads the guest's memory (memSample) and
// memPolicy.decide says what the VM should have: what its programs use,
// without the page cache, plus headroom for what starts next — at least
// memHeadroomMin, and memHeadroomPerAgent for each busy agent (a turn of its
// chat running, or a heavy command), since a busy agent's machine can grow by
// that much in the time it takes to notice, memHeadroomIdleAgent for each
// idle one, which grows little until its next turn starts, and
// what use would reach in memLookahead at the rate it has lately been rising,
// so a burst (a build's linker, a test suite's browsers) finds its memory
// already there rather than stalling a few hundred MiB at a time.
// Growing is done at once (it takes the guest under a second); memory stalls
// (PSI) grow it by memPressureStep when it has less than memHeadroomMin
// available, since they mean the guest is already short. Stalls with plenty
// available aren't for want of memory the VM can be given: the kernel's own
// caches (dentries, inodes) live only in the memory it booted with, and
// hotplugged memory, onlined movable, can't hold them, so growing then
// would only be shrunk again a minute later. Shrinking waits for the target to
// have stayed lower for memShrinkAfter, so a build that stops for a minute
// doesn't hand memory back only to ask for it again, and first drops the
// guest's clean page cache, which would otherwise hold the blocks it has to
// unplug. What the host gets back afterwards, free page reporting (the
// balloon's) returns even without a shrink.
//
// Free page reporting only returns free memory, and only in whole 2 MiB
// blocks: a guest that has stopped working keeps its page cache and kernel
// caches, and scattered free pages, resident on the host. So once the VM is
// quiet — no agent running for memNoAgentsAfter, or its CPUs all but idle for
// memIdleAfter — and the host holds more than memReclaimMin beyond what it
// uses, the policy asks for a reclaim: drop the page cache and the kernel's
// reclaimable caches, and compact what's free into whole blocks, which the
// balloon then hands back within seconds. At most every memReclaimEvery, so a
// VM that's merely idle between builds isn't made to read everything again
// from the host's disk each time.
//
// While agents are busy the guest's page cache grows into whatever the VM
// has been given, and stays resident on the host however short the host gets:
// the host can't reclaim it, only the guest can. So when the host is short —
// stalling on memory (its own PSI above memHostPressure) or with less than
// memHostAvailableShare of its memory available — or the guest's file cache
// holds more than the allowance a build needs (cacheAllowance) and the host
// holds that too, the policy asks for a trim: the guest reclaims up to memTrimMax of its inactive file pages (memory.reclaim
// on its root cgroup, which takes the coldest first and, with swappiness=0,
// never swaps), then compacts, and free page reporting returns it. A build
// keeps the files it keeps reading, which are on the active list. At most
// every memTrimEvery under pressure and memTrimIdleEvery otherwise.
//
// Headroom isn't kept resident on the host: it's there for growth, which is
// instant, and cache filling it would otherwise stay on the host for as long
// as the agents keep working, since free page reporting only returns free
// memory.

const (
	memHeadroomMin      = 2 * GiB
	memHeadroomPerAgent = 1 * GiB
	// memHeadroomIdleAgent is the headroom for an agent whose machine runs
	// with nothing to do: its next turn starting is seen within
	// memBusyEvery and grows the VM then.
	memHeadroomIdleAgent = 256 << 20
	memPressureStep      = 2 * GiB
	// memPressureSome is the PSI "some avg10" percentage above which the
	// guest is taken to be short of memory.
	memPressureSome = 10.0
	// memGranule is what targets are rounded up to: a multiple of every
	// virtio-mem block size, and coarse enough that small swings in use
	// don't resize the VM back and forth.
	memGranule = 512 << 20
	// memShrinkMin is the least worth shrinking by.
	memShrinkMin    = 1 * GiB
	memShrinkAfter  = 60 * time.Second
	memGrowCooldown = 5 * time.Second
	memTick         = 500 * time.Millisecond
	// memLookahead is how far ahead of rising use the VM is grown: the time
	// from a sample to the guest having the memory online, with margin.
	memLookahead = 3 * time.Second
	// memRateWindow is how long the fastest recent rise is remembered, so
	// one flat sample in a burst doesn't drop the headroom it needs.
	memRateWindow = 2 * time.Second

	memNoAgentsAfter = 30 * time.Second
	memIdleAfter     = 2 * time.Minute
	// memIdleBusy is the share of the guest's CPU time, smoothed over about
	// half a minute, below which it's idle.
	memIdleBusy     = 0.05
	memReclaimMin   = 1 * GiB
	memReclaimEvery = 10 * time.Minute

	// memHostPressure is the host's PSI "some avg10" percentage, and
	// memHostAvailableShare the share of its memory available, past which the
	// host is short.
	memHostPressure       = 10.0
	memHostAvailableShare = 0.10
	// memCacheAllowanceMin and memCacheAllowanceShare (of use) are the file
	// cache the VM keeps with the host not short: more than that, which sits
	// in the headroom, is trimmed.
	memCacheAllowanceMin   = 1 * GiB
	memCacheAllowanceShare = 0.25
	memTrimMin             = 256 << 20
	memTrimMax             = 2 * GiB
	memTrimEvery           = 30 * time.Second
	memTrimIdleEvery       = 2 * time.Minute
)

// memSample is the guest's memory, as read from inside it.
type memSample struct {
	Total     int64 // MemTotal: the memory that's plugged and online
	Available int64 // MemAvailable: what could be had without swapping
	// SwapTotal and SwapFree are the swap `agentbox vm swap` gave it, and
	// what of it is free.
	SwapTotal, SwapFree int64
	// Pressure is the share of the last 10 s some task stalled on memory
	// (PSI "some avg10"), in percent.
	Pressure float64
	Agents   int // agents' machines running
	// Idle is how many of them are known to be idle: no turn of their chat
	// running and no heavy command (filled in by the supervisor, from the
	// daemon); 0 when unknown, which counts every agent as busy.
	Idle int
	// AutoOnline is how the guest onlines memory it's given
	// (/sys/devices/system/memory/auto_online_blocks).
	AutoOnline string
	// CPUBusy and CPUTotal are the guest's CPU time since it booted, in
	// clock ticks: all of it, and all but idle and iowait (/proc/stat).
	CPUBusy, CPUTotal uint64
	// FileCache is the guest's page cache of files (Active(file) and
	// Inactive(file)), and InactiveFile the part of it not used lately.
	FileCache, InactiveFile int64
	// Resident is what the VM holds of the host's memory (filled in by the
	// supervisor, from the host's side); 0 when unknown.
	Resident int64
	// HostTotal, HostAvailable and HostPressure are the host's MemTotal,
	// MemAvailable and PSI "some avg10" (filled in by the supervisor); 0
	// when unknown.
	HostTotal, HostAvailable int64
	HostPressure             float64
}

// hostShort reports whether the host is short of memory.
func (s memSample) hostShort() bool {
	return s.HostTotal > 0 && (s.HostPressure > memHostPressure || float64(s.HostAvailable) < float64(s.HostTotal)*memHostAvailableShare)
}

// Used is what the guest's programs use, without the page cache.
func (s memSample) Used() int64 { return s.Total - s.Available }

// memPolicy is the sizing rule, a pure function of what it's shown.
type memPolicy struct {
	Min, Cap int64
}

// memState is what the policy remembers from one tick to the next.
type memState struct {
	Requested int64     // what the VM was last asked to have
	lastGrow  time.Time // when it was last grown
	lowSince  time.Time // since when the target has been below Requested; zero when it isn't
	lowMax    int64     // the highest target seen since lowSince

	prevUsed int64     // the last sample's use
	prevAt   time.Time // and when it was taken
	rate     float64   // the fastest use has risen lately, bytes a second
	rateAt   time.Time // when rate was measured

	prevBusy, prevTotal uint64
	busy                float64   // the guest's CPU use, smoothed
	idleSince           time.Time // since when busy has been under memIdleBusy
	noAgentsSince       time.Time // since when no agent has run
	lastReclaim         time.Time
	lastTrim            time.Time
}

// memDecision is what to do about it: nothing when Target is 0.
type memDecision struct {
	Target int64
	// Shrink is set when Target is less than what the VM has: the guest's
	// page cache is dropped first, so its blocks can be unplugged.
	Shrink bool
	// Reclaim asks for the guest's caches to be dropped and its free memory
	// compacted, so free page reporting can return it: with or without a
	// Target.
	Reclaim bool
	// Trim, set without Reclaim, is how much of its inactive file cache the
	// guest should reclaim before compacting; HostShort says the host was
	// short of memory when it was asked.
	Trim      int64
	HostShort bool
	Reason    string
}

// target is what the VM should have for s: what it uses plus headroom,
// rounded up to memGranule, within Min and Cap.
func (p memPolicy) target(s memSample, rate float64) int64 {
	return p.clamp(s.Used() + max(s.headroom(), int64(rate*memLookahead.Seconds())))
}

// busy is how many agents' machines are running with work under way.
func (s memSample) busy() int { return s.Agents - min(max(s.Idle, 0), s.Agents) }

// headroom is what the VM keeps beyond use for its agents to grow into.
func (s memSample) headroom() int64 {
	return max(memHeadroomMin, int64(s.busy())*memHeadroomPerAgent+int64(s.Agents-s.busy())*memHeadroomIdleAgent)
}

// cacheAllowance is the file cache the VM keeps while the host isn't short.
func (s memSample) cacheAllowance() int64 {
	return max(memCacheAllowanceMin, int64(float64(s.Used())*memCacheAllowanceShare))
}

// observe keeps what decide needs from one sample to the next: how fast use
// is rising, how busy the guest is, and since when no agent has run.
func (st *memState) observe(s memSample, now time.Time) {
	if !st.prevAt.IsZero() {
		if dt := now.Sub(st.prevAt).Seconds(); dt > 0 {
			r := max(float64(s.Used()-st.prevUsed)/dt, 0)
			if r >= st.rate || now.Sub(st.rateAt) > memRateWindow {
				st.rate, st.rateAt = r, now
			}
		}
	}
	st.prevUsed, st.prevAt = s.Used(), now

	if st.prevTotal > 0 && s.CPUTotal > st.prevTotal && s.CPUBusy >= st.prevBusy {
		busy := float64(s.CPUBusy-st.prevBusy) / float64(s.CPUTotal-st.prevTotal)
		// About half a minute's worth of samples.
		const alpha = 0.02
		st.busy = st.busy*(1-alpha) + busy*alpha
		switch {
		case st.busy >= memIdleBusy:
			st.idleSince = time.Time{}
		case st.idleSince.IsZero():
			st.idleSince = now
		}
	} else if st.prevTotal == 0 {
		st.busy = 1 // not idle until shown to be
	}
	st.prevBusy, st.prevTotal = s.CPUBusy, s.CPUTotal

	switch {
	case s.Agents > 0:
		st.noAgentsSince = time.Time{}
	case st.noAgentsSince.IsZero():
		st.noAgentsSince = now
	}
}

// quiet reports whether the VM has stopped working: no agent running for a
// while, or its CPUs all but idle for longer.
func (st *memState) quiet(now time.Time) bool {
	return !st.noAgentsSince.IsZero() && now.Sub(st.noAgentsSince) >= memNoAgentsAfter ||
		!st.idleSince.IsZero() && now.Sub(st.idleSince) >= memIdleAfter
}

func (p memPolicy) clamp(n int64) int64 {
	n = (n + memGranule - 1) / memGranule * memGranule
	return min(max(n, p.Min), p.Cap)
}

// decide says what to do about sample s at now, and remembers it in st. A nil
// s, a guest that can't be read yet, leaves the VM as it is.
func (p memPolicy) decide(st *memState, s *memSample, now time.Time) memDecision {
	if s == nil {
		return memDecision{}
	}
	st.observe(*s, now)
	want := p.target(*s, st.rate)
	reason := fmt.Sprintf("uses %s with %d agents running, %d busy", gib(s.Used()), s.Agents, s.busy())
	if st.rate*memLookahead.Seconds() > float64(s.headroom()) {
		reason = fmt.Sprintf("uses %s, rising %s a second", gib(s.Used()), gib(int64(st.rate)))
	}
	if s.Pressure > memPressureSome && s.Available < memHeadroomMin && now.Sub(st.lastGrow) >= memGrowCooldown {
		if bumped := p.clamp(st.Requested + memPressureStep); bumped > want {
			want = bumped
			reason = fmt.Sprintf("stalled on memory %.0f%% of the last 10s", s.Pressure)
		}
	}
	var d memDecision
	switch {
	case want > st.Requested:
		st.Requested, st.lastGrow = want, now
		st.lowSince, st.lowMax = time.Time{}, 0
		d = memDecision{Target: want, Reason: reason}
	case want <= st.Requested-memShrinkMin:
		if st.lowSince.IsZero() {
			st.lowSince, st.lowMax = now, want
		}
		st.lowMax = max(st.lowMax, want)
		if now.Sub(st.lowSince) >= memShrinkAfter {
			d = memDecision{Target: st.lowMax, Shrink: true, Reason: reason + fmt.Sprintf(" for %s", memShrinkAfter)}
			st.Requested = st.lowMax
			st.lowSince, st.lowMax = time.Time{}, 0
		}
	default:
		st.lowSince, st.lowMax = time.Time{}, 0
	}
	switch {
	case d.Shrink:
		// Its blocks can only be unplugged once what's in them is gone.
		d.Reclaim, st.lastReclaim = true, now
	case d.Target == 0 && s.Resident > 0 && s.Resident-s.Used() > memReclaimMin &&
		st.quiet(now) && now.Sub(st.lastReclaim) >= memReclaimEvery:
		d.Reclaim, st.lastReclaim = true, now
		d.Reason = fmt.Sprintf("quiet, and holding %s of this machine's memory for %s in use", gib(s.Resident), gib(s.Used()))
	case d.Target == 0 && s.Resident > 0:
		d.Trim, d.HostShort, d.Reason = st.trim(*s, now)
	}
	return d
}

// trim is how much of the guest's inactive file cache to reclaim while it's
// busy, and why: 0 unless the host is short of memory or holds cache beyond
// use and cacheAllowance, and never more than memTrimMax at once.
func (st *memState) trim(s memSample, now time.Time) (int64, bool, string) {
	var n int64
	var every time.Duration
	var reason string
	short := s.hostShort()
	switch {
	case short && s.FileCache > memReclaimMin:
		n, every = s.InactiveFile, memTrimEvery
		reason = fmt.Sprintf("this machine has %s available, and the VM caches %s of files", gib(s.HostAvailable), gib(s.FileCache))
		if s.HostPressure > memHostPressure {
			reason = fmt.Sprintf("this machine stalled on memory %.0f%% of the last 10s, and the VM caches %s of files", s.HostPressure, gib(s.FileCache))
		}
	case s.FileCache > s.cacheAllowance() && s.Resident-s.Used() > s.cacheAllowance():
		// What this machine holds beyond use and the allowance is cache in
		// the headroom (or free pages not yet returned, which the compaction
		// after the trim hands back too).
		allow := s.cacheAllowance()
		n, every = min(s.InactiveFile, s.FileCache-allow, s.Resident-s.Used()-allow), memTrimIdleEvery
		reason = fmt.Sprintf("the VM caches %s of files, more than the %s kept beside %s in use", gib(s.FileCache), gib(allow), gib(s.Used()))
	}
	n = min(n, memTrimMax)
	if n < memTrimMin || now.Sub(st.lastTrim) < every || now.Sub(st.lastReclaim) < every {
		return 0, false, ""
	}
	st.lastTrim = now
	return n, short, reason
}

func gib(n int64) string { return fmt.Sprintf("%.1f GiB", float64(n)/float64(GiB)) }

// memScript prints what parseMemSample reads, in one ssh round trip. An
// agent's machine is a cgroup of its own while it runs: lxc.payload.<name>,
// or agentbox/<name> where an earlier release's shared budget started it,
// beside its .monitor.
const memScript = `cat /proc/meminfo /proc/pressure/memory 2>/dev/null
head -n1 /proc/stat
echo "auto_online $(cat /sys/devices/system/memory/auto_online_blocks 2>/dev/null)"
echo "agents $(ls -d /sys/fs/cgroup/lxc.payload.* /sys/fs/cgroup/agentbox/*/ 2>/dev/null | grep -vc '\.monitor/*$')"`

// parseMemSample reads memScript's output.
func parseMemSample(out string) (memSample, error) {
	var s memSample
	var seen int
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:", "MemAvailable:":
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return s, fmt.Errorf("reading the VM's %s %w", f[0], err)
			}
			if f[0] == "MemTotal:" {
				s.Total = kb << 10
			} else {
				s.Available = kb << 10
			}
			seen++
		case "SwapTotal:", "SwapFree:":
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			if f[0] == "SwapTotal:" {
				s.SwapTotal = kb << 10
			} else {
				s.SwapFree = kb << 10
			}
		case "some":
			s.Pressure = psiSome(sc.Text())
		case "Active(file):", "Inactive(file):":
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			s.FileCache += kb << 10
			if f[0] == "Inactive(file):" {
				s.InactiveFile = kb << 10
			}
		case "cpu":
			// user nice system idle iowait irq softirq steal, then guest
			// time, which user already counts.
			for i, v := range f[1:min(len(f), 9)] {
				n, _ := strconv.ParseUint(v, 10, 64)
				s.CPUTotal += n
				if i != 3 && i != 4 {
					s.CPUBusy += n
				}
			}
		case "auto_online":
			s.AutoOnline = f[1]
		case "agents":
			s.Agents, _ = strconv.Atoi(f[1])
		}
	}
	if seen != 2 || s.Total <= 0 {
		return s, fmt.Errorf("couldn't read the VM's memory from %q", firstLine(out))
	}
	return s, nil
}

// psiSome is the "some avg10" percentage of a /proc/pressure file, 0 when
// it hasn't one.
func psiSome(psi string) float64 {
	for line := range strings.Lines(psi) {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "some" {
			continue
		}
		for _, kv := range f[1:] {
			if v, ok := strings.CutPrefix(kv, "avg10="); ok {
				n, _ := strconv.ParseFloat(v, 64)
				return n
			}
		}
	}
	return 0
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
