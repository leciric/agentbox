package agent

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// The disk guard keeps AgentBox from ever filling the user's disk. It watches
// every file system AgentBox writes to — the storage pool its machines live
// on, the worktrees, its own data, and in the VM the host's disk that holds
// the VM's sparse disk images — and keeps a floor of free space on each:
//
//   - nearing the floor (under DiskWarnFactor times it) it warns, in the
//     app's top bar and on the event stream;
//   - at the floor it refuses what would write a lot more (new agents, forks,
//     image builds, saved bases), keeps queued agents waiting, and pauses the
//     agents writing the most, one each check, until free space stops
//     falling;
//   - back above the warning line it resumes the agents it paused.
//
// It never stops, kills or deletes anything, and it doesn't throttle: #108
// capped the agents' writes with io.max and froze the user's desktop.
//
// DiskGuard is only the decisions, fed numbers: the daemon measures the disks
// and the agents' writes, and does what a Step says (daemon/diskguard.go).

// Default floor: the larger of DefaultDiskFloorMin and DefaultDiskFloorPercent
// of the disk.
const (
	DefaultDiskFloorMin     = int64(10) << 30
	DefaultDiskFloorPercent = 5.0
	// MinDiskFloor is the least the floor may be set to: below it there is no
	// room left to react in before a disk is full.
	MinDiskFloor = int64(2) << 30
	// MaxDiskFloorPercent is the most of a disk the floor may be set to.
	MaxDiskFloorPercent = 50.0
	// DiskWarnFactor is where the guard starts warning, as a multiple of the
	// floor, and where agents it paused are resumed: the gap between the two
	// is what keeps an agent from being paused and resumed on every check.
	DiskWarnFactor = 1.5
	// diskFloorShare holds DiskFloor.Min to a quarter of a small disk, such
	// as the VM's own 20 GiB system disk, which a 10 GiB floor would hold at
	// full forever.
	diskFloorShare = 4
	// diskWriting is the least an agent must have written since the last
	// check to be paused as one of the agents filling the disk.
	diskWriting = int64(1) << 20
)

// DiskFloor is the free space the guard keeps on each disk: the larger of Min
// bytes and Percent of the disk's size.
type DiskFloor struct {
	Min     int64
	Percent float64
}

// DefaultDiskFloor is the floor nobody has chosen.
var DefaultDiskFloor = DiskFloor{Min: DefaultDiskFloorMin, Percent: DefaultDiskFloorPercent}

// Validate refuses a floor the guard couldn't keep.
func (f DiskFloor) Validate() error {
	if f.Min < MinDiskFloor {
		return fmt.Errorf("the disk floor is at least %s; %s is too little to react in", HumanBytes(MinDiskFloor), HumanBytes(f.Min))
	}
	if f.Percent < 0 || f.Percent > MaxDiskFloorPercent {
		return fmt.Errorf("the disk floor's share of a disk is from 0%% to %.0f%%; %g%% isn't", MaxDiskFloorPercent, f.Percent)
	}
	return nil
}

// For is the floor on a disk of total bytes. Min is held to a quarter of a
// small disk; Percent, a share already, isn't.
func (f DiskFloor) For(total int64) int64 {
	least := f.Min
	if total > 0 {
		least = min(least, total/diskFloorShare)
	}
	return max(least, int64(float64(total)*f.Percent/100))
}

// DiskSpace is one disk the guard watches, as measured.
type DiskSpace struct {
	// Label says what's on it, for the user: "Storage pool", "Worktrees".
	Label string
	// Path is what was measured, or "" for the storage pool.
	Path  string
	Free  int64
	Total int64
	// Advice is what frees this disk or gives it room, a sentence each.
	Advice []string
}

// Disk levels, from fine to at the floor.
const (
	DiskOK   = "ok"
	DiskLow  = "low"
	DiskFull = "full"
)

// DiskCheck is one disk with its floor and where it stands.
type DiskCheck struct {
	DiskSpace
	Floor int64
	Level string
}

// DiskWriter is one running agent, with everything its machine has written so
// far (its cgroup's io.stat), which the guard turns into what it wrote since
// the last check.
type DiskWriter struct {
	Ref      string
	Instance string
	Written  int64
	// Paused says its machine is frozen, by whoever.
	Paused bool
}

// DiskStatus is what the guard last found.
type DiskStatus struct {
	// Level is the worst of the disks'.
	Level string
	Disks []DiskCheck
	// Paused are the agents the guard paused and hasn't resumed yet.
	Paused []string
	// Since is when Level last changed.
	Since time.Time
}

// Worst is the disk furthest under its floor, relative to it, or false when
// every disk is fine.
func (s DiskStatus) Worst() (DiskCheck, bool) {
	var worst DiskCheck
	found := false
	for _, d := range s.Disks {
		if d.Level == DiskOK {
			continue
		}
		if !found || float64(d.Free)/float64(max(d.Floor, 1)) < float64(worst.Free)/float64(max(worst.Floor, 1)) {
			worst, found = d, true
		}
	}
	return worst, found
}

// Refusal is the error for starting what that would write a lot, or nil when
// every disk is above its floor.
func (s DiskStatus) Refusal(what string) error {
	if s.Level != DiskFull {
		return nil
	}
	d, _ := s.Worst()
	where := d.Label
	if d.Path != "" {
		where += " (" + d.Path + ")"
	}
	return fmt.Errorf("not %s: %s has %s free, under the %s AgentBox keeps free so your disk never fills. Free some space — destroy agents you're done with, or delete files — or lower the floor in Settings", what, where, HumanBytes(d.Free), HumanBytes(d.Floor))
}

// Summary is one line on where the disks stand, for the log and for a chat.
func (s DiskStatus) Summary() string {
	d, ok := s.Worst()
	if !ok {
		return "every disk AgentBox writes to has more than its floor free"
	}
	where := d.Label
	if d.Path != "" {
		where += " (" + d.Path + ")"
	}
	line := fmt.Sprintf("%s has %s free of %s, and AgentBox keeps %s free", where, HumanBytes(d.Free), HumanBytes(d.Total), HumanBytes(d.Floor))
	if len(s.Paused) > 0 {
		line += "; paused for it: " + strings.Join(s.Paused, ", ")
	}
	return line
}

// DiskGuard decides, one check at a time. It isn't safe for concurrent use:
// the daemon's loop is its only caller.
type DiskGuard struct {
	status  DiskStatus
	written map[string]int64 // instance → Written at the last check
	paused  map[string]bool  // refs the guard paused
}

// NewDiskGuard starts with every disk fine, and paused the refs a guard before
// it paused and didn't resume (a daemon restarted while the disk was full),
// so they're resumed when there's room again.
func NewDiskGuard(paused []string) *DiskGuard {
	g := &DiskGuard{status: DiskStatus{Level: DiskOK}, written: map[string]int64{}, paused: map[string]bool{}}
	for _, ref := range paused {
		g.paused[ref] = true
	}
	g.status.Paused = g.pausedList()
	return g
}

// DiskStep is what one check decided.
type DiskStep struct {
	Status DiskStatus
	// Changed says Status moved: a level, or who's paused.
	Changed bool
	// Pause is the agent to pause now, at most one a check.
	Pause *DiskWriter
	// Resume are the agents the guard paused to resume, now there's room.
	Resume []string
}

// Step takes one check's numbers and says what to do. spaces are the disks
// as measured; one that couldn't be measured is left out, rather than taken
// for full or fine. writers are the running and paused agents, or nil when
// they couldn't be listed (Incus doesn't answer): the levels, and so the
// warnings and refusals, still move, but nobody is paused or resumed.
func (g *DiskGuard) Step(now time.Time, floor DiskFloor, spaces []DiskSpace, writers []DiskWriter) DiskStep {
	prev := g.status
	level := DiskOK
	checks := make([]DiskCheck, 0, len(spaces))
	for _, d := range spaces {
		c := DiskCheck{DiskSpace: d, Floor: floor.For(d.Total), Level: DiskOK}
		switch {
		case d.Free < c.Floor:
			c.Level = DiskFull
		case float64(d.Free) < float64(c.Floor)*DiskWarnFactor:
			c.Level = DiskLow
		}
		checks = append(checks, c)
		level = worseDisk(level, c.Level)
	}

	var step DiskStep
	if writers == nil {
		g.status = DiskStatus{Level: level, Disks: checks, Paused: g.pausedList(), Since: prev.Since}
		if level != prev.Level || g.status.Since.IsZero() {
			g.status.Since = now
		}
		step.Status = g.status
		step.Changed = level != prev.Level
		return step
	}

	// What each agent wrote since the last check. A first sight, or a
	// counter that went backwards (a machine restarted), counts nothing.
	delta := map[string]int64{}
	seen := map[string]bool{}
	for _, w := range writers {
		seen[w.Instance] = true
		if before, ok := g.written[w.Instance]; ok && w.Written >= before {
			delta[w.Instance] = w.Written - before
		}
		g.written[w.Instance] = w.Written
	}
	for inst := range g.written {
		if !seen[inst] {
			delete(g.written, inst)
		}
	}

	byRef := map[string]DiskWriter{}
	for _, w := range writers {
		byRef[w.Ref] = w
	}
	for ref := range g.paused {
		w, ok := byRef[ref]
		if !ok || !w.Paused {
			// Stopped, destroyed or resumed by someone else since: not the
			// guard's to resume any more.
			delete(g.paused, ref)
		}
	}
	switch level {
	case DiskOK:
		for ref := range g.paused {
			step.Resume = append(step.Resume, ref)
			delete(g.paused, ref)
		}
		sort.Strings(step.Resume)
	case DiskFull:
		var top *DiskWriter
		for _, w := range writers {
			if w.Paused || delta[w.Instance] < diskWriting {
				continue
			}
			if top == nil || delta[w.Instance] > delta[top.Instance] {
				top = &w
			}
		}
		if top != nil {
			step.Pause = top
			g.paused[top.Ref] = true
		}
	}

	g.status = DiskStatus{Level: level, Disks: checks, Paused: g.pausedList(), Since: prev.Since}
	if level != prev.Level || g.status.Since.IsZero() {
		g.status.Since = now
	}
	step.Status = g.status
	step.Changed = level != prev.Level || !slices.Equal(g.status.Paused, prev.Paused)
	return step
}

// Unpause forgets that the guard paused ref: its pause failed, or the user
// stopped it.
func (g *DiskGuard) Unpause(ref string) {
	delete(g.paused, ref)
	g.status.Paused = g.pausedList()
}

// PausedByGuard reports whether the guard paused ref and hasn't resumed it.
func (g *DiskGuard) PausedByGuard(ref string) bool { return g.paused[ref] }

// Status is what the last check found.
func (g *DiskGuard) Status() DiskStatus { return g.status }

func (g *DiskGuard) pausedList() []string {
	out := make([]string, 0, len(g.paused))
	for ref := range g.paused {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func worseDisk(a, b string) string {
	rank := map[string]int{DiskOK: 0, DiskLow: 1, DiskFull: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
