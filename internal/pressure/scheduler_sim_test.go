package pressure

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

// A simulated VM, stepped the way the daemon steps the governor and
// admission (every 2 seconds), to check the scheduler as a whole: agents
// boot only into memory that is there, builds start as they fit and wait
// in line otherwise (the viewed agent first), the newest is paused and its
// memory pushed out to swap when running ones outgrow the VM, paused ones
// resume in order, idle agents give their memory back before a build waits,
// and nothing is ever killed but a build bigger than its own agent's limit.
//
// The kernel is modelled: past what the VM has, it pushes cold pages (what
// idle agents and paused builds hold) out to zram first, stalling some
// tasks (PSI some); out of those, it pushes out pages in use, stalling all
// (PSI full); and with swap full too, it kills the biggest process, which
// the test counts as a failure of the scheduler.

const simStep = 2 * time.Second

type simBuild struct {
	key        string
	peak, rate int64
	work       time.Duration
	// At is how long after the previous build ends (or the agent boots)
	// this one asks.
	at time.Duration

	asked, started, ended time.Time
	done                  time.Duration
	used, swapped         int64
	paused, killed        bool
	pauses                int
}

type simAgent struct {
	ref  string
	made time.Time
	// hot is what its AI tool and the rest hold and keep touching; cold
	// what it holds and doesn't (pushed out as it idles).
	hot, cold     int64
	coldOut       int64
	booted, asked bool
	bootedAt      time.Time
	stopped       bool // its machine stopped after its work, On hold
	builds        []*simBuild
	next          int // the build it is on
	idleSince     time.Time
	viewed        bool
	limit         int64
}

func (a *simAgent) current() *simBuild {
	if a.next < len(a.builds) {
		return a.builds[a.next]
	}
	return nil
}

func (a *simAgent) resident() int64 {
	if !a.booted || a.stopped {
		return 0
	}
	n := a.hot + a.cold - a.coldOut
	for _, b := range a.builds {
		if !b.started.IsZero() && b.ended.IsZero() {
			n += b.used - b.swapped
		}
	}
	return n
}

type simVM struct {
	t           *testing.T
	total, swap int64
	swapUsed    int64
	agents      []*simAgent
	g           *Governor
	ad          *Admission
	now         time.Time
	psi         PSI
	vmKills     int
	ownKills    []string
	log         []string
	reclaimed   int // idle reclaims before a build waited
}

func newSimVM(t *testing.T, total, swap float64) *simVM {
	return &simVM{t: t, total: gibs(total), swap: gibs(swap), g: NewGovernor(DefaultPolicy), ad: NewAdmission(DefaultPolicy), now: t0}
}

func (v *simVM) resident() int64 {
	var n int64
	for _, a := range v.agents {
		n += a.resident()
	}
	return n
}

func (v *simVM) mem() Mem {
	avail := v.total - v.resident()
	return Mem{Total: v.total, Cap: v.total, Available: max(avail, 0), SwapTotal: v.swap, SwapFree: v.swap - v.swapUsed}
}

func (v *simVM) toSwap(want int64) int64 {
	n := min(want, v.swap-v.swapUsed)
	v.swapUsed += max(n, 0)
	return max(n, 0)
}

func (v *simVM) find(ref string) *simAgent {
	for _, a := range v.agents {
		if a.ref == ref {
			return a
		}
	}
	return nil
}

func (v *simVM) build(ref, key string) *simBuild {
	if a := v.find(ref); a != nil {
		for _, b := range a.builds {
			if b.key == key {
				return b
			}
		}
	}
	return nil
}

// kernel makes room past what the VM has, as described above, and sets PSI.
func (v *simVM) kernel() {
	v.psi = PSI{}
	deficit := v.resident() - (v.total - gibs(0.1))
	if deficit <= 0 {
		return
	}
	// Cold pages first: idle agents' and paused builds'.
	for _, a := range v.agents {
		if deficit <= 0 {
			break
		}
		if a.resident() == 0 {
			continue
		}
		n := v.toSwap(min(deficit, a.cold-a.coldOut))
		a.coldOut += n
		deficit -= n
		for _, b := range a.builds {
			if b.paused && deficit > 0 {
				n := v.toSwap(min(deficit, b.used-b.swapped))
				b.swapped += n
				deficit -= n
			}
		}
	}
	v.psi.Some.Avg10 = 15
	if deficit <= 0 {
		return
	}
	// Pages in use: everything stalls.
	for _, a := range v.agents {
		for _, b := range a.builds {
			if deficit > 0 && !b.started.IsZero() && b.ended.IsZero() {
				n := v.toSwap(min(deficit, b.used-b.swapped))
				b.swapped += n
				deficit -= n
			}
		}
	}
	v.psi.Some.Avg10, v.psi.Full.Avg10 = 70, 50
	if deficit <= 0 {
		return
	}
	// Nothing left: the biggest process goes.
	var big *simBuild
	var who *simAgent
	for _, a := range v.agents {
		for _, b := range a.builds {
			if !b.started.IsZero() && b.ended.IsZero() && (big == nil || b.used > big.used) {
				big, who = b, a
			}
		}
	}
	v.vmKills++
	if big != nil {
		v.logf("VM out of memory: killed %s/%s at %s", who.ref, big.key, gib(big.used))
		v.end(who, big, true)
	}
}

func (v *simVM) logf(format string, args ...any) {
	v.log = append(v.log, fmt.Sprintf("%4.0fs ", v.now.Sub(t0).Seconds())+fmt.Sprintf(format, args...))
}

func (v *simVM) end(a *simAgent, b *simBuild, killed bool) {
	b.ended, b.killed = v.now, killed
	v.swapUsed -= b.swapped
	b.used, b.swapped = 0, 0
	v.g.End(a.ref, b.key)
	a.next++
	a.idleSince = v.now
}

// step is one 2-second tick: the agents' work, the kernel, then what the
// daemon does with the reading.
func (v *simVM) step() {
	v.now = v.now.Add(simStep)
	for _, a := range v.agents {
		if !a.booted || a.stopped {
			continue
		}
		b := a.current()
		if b == nil {
			// Its work done: idle tiers, its memory out after 2 minutes, its
			// machine stopped (On hold) after 10.
			if v.now.Sub(a.idleSince) >= 2*time.Minute && a.coldOut < a.cold {
				a.coldOut += v.toSwap(a.cold - a.coldOut)
			}
			if v.now.Sub(a.idleSince) >= 10*time.Minute {
				v.swapUsed -= a.coldOut
				a.coldOut, a.stopped = 0, true
				v.logf("%s idle: stopped, On hold", a.ref)
			}
			continue
		}
		switch {
		case b.asked.IsZero() && v.now.Sub(a.idleSince) >= b.at:
			b.asked = v.now
			_, started := v.g.Ask(a.ref, b.key, b.key, time.Hour, v.now)
			if a.viewed {
				v.g.Prefer(a.ref)
			}
			if !started {
				// Before it waits, idle agents' memory goes out to swap.
				for _, o := range v.agents {
					if o != a && o.resident() > 0 && o.current() == nil || (o != a && o.current() != nil && o.current().asked.IsZero()) {
						if o.resident() > 0 && o.coldOut < o.cold {
							o.coldOut += v.toSwap(o.cold - o.coldOut)
							v.reclaimed++
						}
					}
				}
			}
		case !b.started.IsZero() && b.ended.IsZero() && !b.paused:
			if b.swapped > 0 { // paging back what it touches
				back := min(b.swapped, b.rate*2)
				b.swapped -= back
				v.swapUsed -= back
			}
			b.used = min(b.peak, b.used+b.rate*int64(simStep/time.Second))
			b.done += simStep
			if a.hot+a.cold+b.used > a.limit {
				v.ownKills = append(v.ownKills, a.ref+"/"+b.key)
				v.logf("%s/%s killed at its agent's own limit (%s)", a.ref, b.key, gib(a.limit))
				v.end(a, b, true)
			} else if b.done >= b.work {
				v.end(a, b, false)
			}
		}
	}
	v.kernel()
	s := Sample{PSI: v.psi, Mem: v.mem()}
	acts := v.g.Step(v.now, s)
	for _, r := range acts.Start {
		b := v.build(r.Agent, r.Key)
		b.started = v.now
		v.g.Place(r.Agent, r.Key)
		v.logf("start %s/%s (%s available)", r.Agent, r.Key, gib(s.Mem.Available))
	}
	for _, r := range acts.Pause {
		b := v.build(r.Agent, r.Key)
		b.paused = true
		b.pauses++
		// Frozen, and its memory pushed out (memory.reclaim): a frozen
		// cgroup that keeps its RAM frees nothing.
		b.swapped += v.toSwap(b.used - b.swapped)
		v.logf("pause %s/%s (%s): %s pushed out", r.Agent, r.Key, r.Why, gib(b.swapped))
	}
	for _, r := range acts.Resume {
		v.build(r.Agent, r.Key).paused = false
		v.logf("resume %s/%s", r.Agent, r.Key)
	}
	held := len(v.g.Waiting()) > 0
	for _, r := range v.g.Running() {
		held = held || r.Paused
	}
	running := 0
	for _, a := range v.agents {
		if a.booted && !a.stopped {
			running++
		}
	}
	for _, ref := range v.ad.Step(v.now, v.mem(), held, running) {
		a := v.find(ref)
		a.booted, a.bootedAt, a.idleSince = true, v.now, v.now
		v.logf("boot %s (%s available)", ref, gib(s.Mem.Available))
	}
}

func (v *simVM) settled() bool {
	for _, a := range v.agents {
		if !a.booted || a.current() != nil {
			return false
		}
	}
	return true
}

func (v *simVM) run(limit time.Duration) {
	for v.now.Sub(t0) < limit && !v.settled() {
		v.step()
	}
}

func (v *simVM) dump() {
	for _, l := range v.log {
		v.t.Log(l)
	}
}

// The case the user asked for: 10 agents running, 2 GB free, 4 of them
// start builds at once that together need far more than there is. The one
// the user looks at starts first, the rest wait in line and start as they
// fit, the newest is paused and pushed out if those running outgrow the VM,
// and all of them finish with nothing killed.
func TestSchedulerTenAgentsFourBuilds(t *testing.T) {
	v := newSimVM(t, 16, 4)
	for i := range 10 {
		a := &simAgent{ref: fmt.Sprintf("p/a%d", i), made: t0.Add(time.Duration(i) * time.Minute), hot: gibs(0.8), cold: gibs(0.6), limit: gibs(8)}
		a.booted, a.asked, a.idleSince = true, true, t0
		if i < 4 {
			a.builds = []*simBuild{{key: "build", peak: gibs(3), rate: 100 << 20, work: 90 * time.Second}}
			a.idleSince = t0.Add(-time.Hour)
		} else {
			// The others sit between turns: no build, not yet stopped.
			a.idleSince = t0.Add(time.Hour)
		}
		v.agents = append(v.agents, a)
	}
	v.agents[3].viewed = true
	if avail := v.mem().Available; avail < gibs(1.99) || avail > gibs(2.01) {
		t.Fatalf("the VM starts with %s available, want 2 GiB", gib(avail))
	}
	v.run(30 * time.Minute)
	defer v.dump() // the timeline, with -v
	var order []string
	for _, a := range v.agents[:4] {
		b := a.builds[0]
		if b.started.IsZero() || b.ended.IsZero() || b.killed {
			t.Errorf("%s: started %v, ended %v, killed %v", a.ref, b.started, b.ended, b.killed)
		}
	}
	builders := append([]*simAgent(nil), v.agents[:4]...)
	sort.SliceStable(builders, func(i, j int) bool { return builders[i].builds[0].started.Before(builders[j].builds[0].started) })
	for _, a := range builders {
		order = append(order, a.ref)
	}
	if order[0] != "p/a3" {
		t.Errorf("started in the order %v, want the viewed p/a3 first", order)
	}
	if fmt.Sprint(order[1:]) != "[p/a0 p/a1 p/a2]" {
		t.Errorf("then %v, want the rest in the order they asked", order[1:])
	}
	if v.vmKills > 0 || len(v.ownKills) > 0 {
		t.Errorf("killed: %d by the VM, %v at their own limit", v.vmKills, v.ownKills)
	}
	if v.reclaimed == 0 {
		t.Error("no idle agent's memory went out before a build waited")
	}
	// They didn't all run at once: what didn't fit waited.
	first := v.agents[3].builds[0]
	for _, a := range v.agents[:3] {
		if b := a.builds[0]; b.started.Before(first.started.Add(simStep)) {
			t.Errorf("%s started %s after the first, with no room for it", a.ref, b.started.Sub(first.started))
		}
	}
}

// A build bigger than its agent can ever have is killed at its own limit,
// and nothing else: the VM never runs out.
func TestSchedulerKillsAnOversizedBuildAtItsOwnLimit(t *testing.T) {
	v := newSimVM(t, 16, 4)
	for i := range 3 {
		a := &simAgent{ref: fmt.Sprintf("p/a%d", i), made: t0, hot: gibs(0.8), cold: gibs(0.6), limit: gibs(8), booted: true, asked: true, idleSince: t0.Add(-time.Hour)}
		a.builds = []*simBuild{{key: "build", peak: gibs(2), rate: 100 << 20, work: time.Minute}}
		v.agents = append(v.agents, a)
	}
	v.agents[1].builds[0].peak, v.agents[1].builds[0].work = gibs(12), 5*time.Minute
	v.run(time.Hour)
	if len(v.ownKills) != 1 || v.ownKills[0] != "p/a1/build" || v.vmKills > 0 {
		v.dump()
		t.Fatalf("killed %v at their own limit, %d by the VM; want only p/a1's", v.ownKills, v.vmKills)
	}
	for _, a := range []*simAgent{v.agents[0], v.agents[2]} {
		if b := a.builds[0]; b.ended.IsZero() || b.killed {
			t.Errorf("%s's build: ended %v, killed %v", a.ref, b.ended, b.killed)
		}
	}
}

// Thirty agents started at once on a VM that holds about a dozen: they boot
// as memory allows, oldest first, each runs its builds, those idle give their
// memory back and stop On hold, and every one of them boots and finishes with
// nothing killed.
func TestSchedulerThirtyAgents(t *testing.T) {
	v := newSimVM(t, 24, 6)
	for i := range 30 {
		a := &simAgent{ref: fmt.Sprintf("p/a%02d", i), made: t0.Add(time.Duration(i) * time.Second), hot: gibs(0.7), cold: gibs(0.6), limit: gibs(12)}
		for j := range 3 {
			a.builds = append(a.builds, &simBuild{
				key:  fmt.Sprintf("b%d", j),
				peak: gibs(1 + float64((i*7+j*3)%5)/2), // 1 to 3 GiB
				rate: int64(60+(i*13+j)%80) << 20,
				work: time.Duration(40+(i*11+j*17)%80) * time.Second,
				at:   time.Duration(30+(i*5+j*7)%90) * time.Second, // thinking, waiting on the model
			})
		}
		v.agents = append(v.agents, a)
		v.ad.Ask(a.ref, a.made, t0)
	}
	v.agents[17].viewed = true
	v.run(6 * time.Hour)
	defer func() {
		if t.Failed() {
			v.dump()
		}
	}()
	if !v.settled() {
		for _, a := range v.agents {
			if !a.booted || a.current() != nil {
				t.Errorf("%s: booted %v, on build %d of %d", a.ref, a.booted, a.next, len(a.builds))
			}
		}
	}
	if v.vmKills > 0 || len(v.ownKills) > 0 {
		t.Errorf("killed: %d by the VM, %v at their own limit", v.vmKills, v.ownKills)
	}
	// Oldest first.
	for i := 1; i < len(v.agents); i++ {
		if v.agents[i].bootedAt.Before(v.agents[i-1].bootedAt) {
			t.Errorf("%s booted before the older %s", v.agents[i].ref, v.agents[i-1].ref)
		}
	}
	for _, a := range v.agents {
		for _, b := range a.builds {
			if b.killed {
				t.Errorf("%s/%s was killed", a.ref, b.key)
			}
		}
	}
	t.Logf("all 30 finished in %s; %d pauses", v.now.Sub(t0).Round(time.Minute), func() int {
		n := 0
		for _, a := range v.agents {
			for _, b := range a.builds {
				n += b.pauses
			}
		}
		return n
	}())
}
