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
// memHeadroomMin, and memHeadroomPerAgent for each running agent, since an
// agent's machine can grow by that much in the time it takes to notice.
// Growing is done at once (it takes the guest under a second); memory stalls
// (PSI) grow it by memPressureStep even when the numbers look fine, since
// they mean the guest is already short. Shrinking waits for the target to
// have stayed lower for memShrinkAfter, so a build that stops for a minute
// doesn't hand memory back only to ask for it again, and first drops the
// guest's clean page cache, which would otherwise hold the blocks it has to
// unplug. What the host gets back afterwards, free page reporting (the
// balloon's) returns even without a shrink.

const (
	memHeadroomMin      = 2 * GiB
	memHeadroomPerAgent = 1 * GiB
	memPressureStep     = 2 * GiB
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
	memTick         = 2 * time.Second
)

// memSample is the guest's memory, as read from inside it.
type memSample struct {
	Total     int64 // MemTotal: the memory that's plugged and online
	Available int64 // MemAvailable: what could be had without swapping
	// Pressure is the share of the last 10 s some task stalled on memory
	// (PSI "some avg10"), in percent.
	Pressure float64
	Agents   int // agents' machines running
	// AutoOnline is how the guest onlines memory it's given
	// (/sys/devices/system/memory/auto_online_blocks).
	AutoOnline string
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
}

// memDecision is what to do about it: nothing when Target is 0.
type memDecision struct {
	Target int64
	// Shrink is set when Target is less than what the VM has: the guest's
	// page cache is dropped first, so its blocks can be unplugged.
	Shrink bool
	Reason string
}

// target is what the VM should have for s: what it uses plus headroom,
// rounded up to memGranule, within Min and Cap.
func (p memPolicy) target(s memSample) int64 {
	headroom := max(int64(memHeadroomMin), int64(s.Agents)*memHeadroomPerAgent)
	return p.clamp(s.Used() + headroom)
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
	want := p.target(*s)
	reason := fmt.Sprintf("uses %s with %d agents running", gib(s.Used()), s.Agents)
	if s.Pressure > memPressureSome && now.Sub(st.lastGrow) >= memGrowCooldown {
		if bumped := p.clamp(st.Requested + memPressureStep); bumped > want {
			want = bumped
			reason = fmt.Sprintf("stalled on memory %.0f%% of the last 10s", s.Pressure)
		}
	}
	switch {
	case want > st.Requested:
		st.Requested, st.lastGrow = want, now
		st.lowSince, st.lowMax = time.Time{}, 0
		return memDecision{Target: want, Reason: reason}
	case want <= st.Requested-memShrinkMin:
		if st.lowSince.IsZero() {
			st.lowSince, st.lowMax = now, want
		}
		st.lowMax = max(st.lowMax, want)
		if now.Sub(st.lowSince) < memShrinkAfter {
			return memDecision{}
		}
		to := st.lowMax
		st.Requested = to
		st.lowSince, st.lowMax = time.Time{}, 0
		return memDecision{Target: to, Shrink: true, Reason: reason + fmt.Sprintf(" for %s", memShrinkAfter)}
	default:
		st.lowSince, st.lowMax = time.Time{}, 0
		return memDecision{}
	}
}

func gib(n int64) string { return fmt.Sprintf("%.1f GiB", float64(n)/float64(GiB)) }

// memScript prints what parseMemSample reads, in one ssh round trip. An
// agent's machine is a cgroup of its own while it runs: lxc.payload.<name>,
// or agentbox/<name> under the shared budget (internal/agent/budget.go),
// beside its .monitor.
const memScript = `cat /proc/meminfo /proc/pressure/memory 2>/dev/null
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
		case "some":
			for _, kv := range f[1:] {
				if v, ok := strings.CutPrefix(kv, "avg10="); ok {
					s.Pressure, _ = strconv.ParseFloat(v, 64)
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

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
