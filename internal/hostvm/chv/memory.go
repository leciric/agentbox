package chv

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/hostload"
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
// doesn't hand memory back only to ask for it again, and first trims the
// guest's cache beyond its floor (below). What the host gets back
// afterwards, free page reporting (the balloon's) returns even without a
// shrink.
//
// Free page reporting only returns free memory, and only in whole 2 MiB
// blocks: a guest that has stopped working keeps its page cache and kernel
// caches, and scattered free pages, resident on the host. So once the VM is
// quiet — no agent running for memNoAgentsAfter, or, with the host short of
// memory, its CPUs all but idle for memIdleAfter (agents waiting on their
// model are idle, and read everything again on their next turn) — and the
// host holds more than memReclaimMin beyond what it uses, the policy asks
// for a reclaim: drop the page cache and the kernel's
// reclaimable caches, and compact what's free into whole blocks, which the
// balloon then hands back within seconds. At most every memReclaimEvery, so a
// VM that's merely idle between builds isn't made to read everything again
// from the host's disk each time.
//
// The guest's file cache is not headroom to be taken back on a fixed timer:
// with its disks opened direct=on, every page it drops and needs again is read
// from the host's disk. Taking cache back every couple of minutes, down to a
// small allowance, from a VM whose builds keep reading the same toolchains and
// node_modules read the host's NVMe at tens of MB/s, hundreds of GB a day, and
// froze its desktop. So the VM keeps a floor of file cache (cacheFloor) as part
// of what it should have, beside use and headroom: the larger of an allowance
// for a build (cacheAllowance), the guest's active file pages, which it has
// read more than once lately, and what the guest's refaults have shown it
// needs. A refault is a page read again soon after it was dropped
// (workingset_refault_file): while they run above memRefaultHigh the floor is
// too low, so it's raised to the cache it has plus what it is reading again,
// and no trim is asked for memTrimBackoff, doubling while it keeps happening.
// Calm for memFloorCalm, the floor comes down by an eighth and the backoff
// resets.
//
// The cache beyond the floor is trimmed in two cases: the guest reclaims up to
// memTrimMax of its inactive file pages, never past the floor (memory.reclaim
// on its root cgroup, which takes the coldest first, killed after
// trimWriteTimeout), then compacts, and free page reporting returns it.
//   - The host is short of memory — stalling on it (its own PSI above
//     memHostPressure) or with less than it keeps (HostKeep) available — at
//     most every memTrimEvery.
//   - The VM is settled (25 Nov 2026), whether or not the host is short: it
//     has not been resized for memSettleAfter, and its refaults have stayed
//     under memRefaultLow that long (a guest that doesn't report them is
//     never settled), at most every memSettledTrimEvery. A VM of 16 GiB with
//     5.7 GiB in use held 11 GiB of file cache, 8.9 GiB of it inactive, on a
//     host 80% used, and gave none of it back, as free page reporting only
//     returns what's free. What keeps this from being the trim on a timer that
//     froze that desktop is what it never takes: the floor, with every active
//     page in it, and anything a refault has shown the guest reads again,
//     which holds it off for memTrimBackoff.
//
// A shrink trims only the cache beyond the floor too, rather than dropping all
// of it: the guest migrates what's left out of the blocks it unplugs, into the
// floor the target kept room for.
//
// The cap is never more than the host's memory less HostReserve (MaxCap, a
// small fixed 4 GiB), but it is only a ceiling, and the host's own reading
// decides: the host
// keeps HostKeep available for itself, its desktop and the app. What the VM
// may have now is what it holds of the host's memory plus what the host has
// available beyond that (hostCeiling): memory the VM was given but hasn't
// touched yet is in both, so that sum stays the same as the guest fills what
// it has, and a VM within it leaves the host its reserve even once it has
// touched every page. Growing never passes it, and a VM that has more (the
// desktop grew, or the VM grew before it) is shrunk back to it within
// memShrinkAfterHost, never below its use and memHeadroomMin. On 18 Nov 2026
// the VM held 13.8 GiB of a 32 GiB host for 4.8 GiB in use, the host had
// 5 to 7 GiB available of the 8 it keeps, and its kernel killed the VM six
// times in 35 minutes: the cap of 14 GiB had been the target.
//
// Growing is also slowed: at most memGrowMax at a time, memGrowEvery apart
// (after a boot every agent resumed its turn at once, and the VM grew a GiB
// a second), and not at all while the host stalls on memory now or has less
// than it keeps now. Nothing else holds it, and nothing holds it after the
// host has recovered (24 Nov 2026, the user's decision): growth waited
// while hostload judged the host busy for memory and for 30 seconds after,
// and with a reserve of a quarter of a 30.5 GiB host (7.6 GiB) against its
// 7.5 GiB available, it turned busy every minute and held the VM at 7 GiB.
// A busy CPU or disk never held it: the VM's own vCPUs make most of a
// host's CPU stalls while its agents build, and memory costs no CPU; and a
// VM short of memory reads its files again through the shared folder and
// its disks (19 Nov 2026: the host's IO pressure, the VM's own, held the VM
// at 10 GiB while its agents thrashed). What growing takes from the host's
// own cache shows as its memory pressure, which does hold it.
//
// While the host has less than its reserve, the VM gives back: the target
// keeps only its use, memHeadroomMin and the cache allowance (the floor
// yields, refaults or not: the host comes first, and the cache it re-reads
// costs the host's disk, not its memory), it shrinks after memShrinkAfterHost
// by as little as memGranule, and its cache beyond that is trimmed every
// memTrimEvery. A host that stalls on memory gets the same shrink timing and
// trims, but the floor holds. Below hostCriticalAt (hostCritical) it shrinks at
// once to its use and memHeadroomLeast and drops its caches: an agent's
// process the guest kills is better than the host's kernel killing the VM.
// Only the host under its hard floor holds boots (HoldBoots, now: under its
// reserve no longer does, as on 19 Nov 2026 a host with 8.2 GiB free under
// a 7.6 GiB reserve within two minutes held every boot while its own memory
// pressure read 0), which the supervisor tells the daemon.
//
// The last resort is the host's hard floor (hostFloor, the larger of 2 GiB
// and 6% of it): with less than that available for memEmergencyAfter, the
// supervisor has the daemon freeze and then stop the agent that holds the
// most memory (Shed), one every memShedEvery while it lasts, rather than
// leave the host to freeze or its kernel to kill the VM. The shrink to lean
// has already been asked for by then (half the reserve is more than the
// floor), and follows what the stopped agent gave back.
//
// Once every agent has been idle for memIdleAfterAll (no turn of its chat and
// no heavy command, by the daemon), the target keeps only the allowance too,
// so the VM shrinks after memShrinkAfter, and its caches are dropped.
//
// Headroom is there for growth, which is instant; the cache floor is part of
// the target, so cache up to it doesn't eat the headroom.

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
	// memGrowMax and memGrowEvery bound how fast the VM grows: half a GiB a
	// second at most, so the host's own reading keeps up with what the guest
	// was given. memSafeGrowMax is memGrowMax in safe mode.
	memGrowMax     = 1 * GiB
	memGrowEvery   = 2 * time.Second
	memSafeGrowMax = 512 << 20
	// memHeadroomLeast is what a shrink for a host below half its reserve
	// leaves beyond use.
	memHeadroomLeast = 1 * GiB
	// memIdleAfterAll is how long every agent must have been idle, by the
	// daemon, before the VM gives back its cache beyond the allowance.
	memIdleAfterAll = 5 * time.Minute
	// memKeepMin and memKeepShare (of its memory) make HostKeep.
	memKeepMin   = 3 * GiB
	memKeepShare = 0.125
	// memEmergencyAfter is how long the host stays under its floor before
	// an agent is stopped, and memShedEvery the least time between two.
	memEmergencyAfter = 3 * time.Second
	memShedEvery      = 20 * time.Second
	memTick           = 500 * time.Millisecond
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

	// memHostPressure is the host's PSI "some avg10" percentage past which
	// the host is short, as it is with less than HostKeep available.
	memHostPressure = hostload.MemoryBusy
	// memShrinkAfterHost is how long a lower target waits to be given back
	// while the host has less than its reserve.
	memShrinkAfterHost = 5 * time.Second
	// memCacheAllowanceMin and memCacheAllowanceShare (of use) are the file
	// cache the VM keeps with the host not short: more than that, which sits
	// in the headroom, is trimmed.
	memCacheAllowanceMin   = 1 * GiB
	memCacheAllowanceShare = 0.25
	memTrimMin             = 256 << 20
	memTrimMax             = 2 * GiB
	memTrimEvery           = 10 * time.Second
	// memSettleAfter is how long the VM must have held still — not resized,
	// its guest not reading dropped pages again (memRefaultLow) — before its
	// cache beyond the floor is trimmed with the host not short, and
	// memSettledTrimEvery the least time between two such trims. The first
	// is the time a build takes to show whether it re-reads what was dropped;
	// the second is what a bad trim costs to find out, a refault storm of up
	// to memTrimMax read from the host's disk, before memTrimBackoff holds
	// the next one off (until #683 it was every 2 minutes, with no floor).
	memSettleAfter      = 3 * time.Minute
	memSettledTrimEvery = 5 * time.Minute

	// memRefaultHigh is the rate of refaults, in bytes a second, past which
	// the guest's cache floor is too low: 2 MiB/s, 30k pages a minute. A VM
	// keeping its working set refaults a few hundred pages a minute.
	memRefaultHigh = 2 << 20
	// memRefaultLow is the rate under which the floor is calm.
	memRefaultLow = 256 << 10
	// memRefaultAlpha smooths the rate over about 10 s of samples.
	memRefaultAlpha = 0.05
	// memRefaultAhead is how much of what it reads again the floor makes
	// room for when raised: a minute's worth.
	memRefaultAhead = time.Minute
	// memFloorRaiseEvery is how often the floor may be raised, so the cache
	// can grow into it before it's raised again.
	memFloorRaiseEvery = 30 * time.Second
	// memFloorCalm is how long refaults stay under memRefaultLow before the
	// floor comes down by an eighth and the trim backoff resets.
	memFloorCalm = 10 * time.Minute
	// memTrimBackoff is how long trims wait after refaults have shown the
	// floor too low, doubling each time up to memTrimBackoffMax.
	memTrimBackoff    = 2 * time.Minute
	memTrimBackoffMax = 30 * time.Minute
	// pageSize is the guest's page, what /proc/vmstat counts in.
	pageSize = 4096
)

// memSample is the guest's memory, as read from inside it.
type memSample struct {
	Total     int64 // MemTotal: the memory that's plugged and online
	Available int64 // MemAvailable: what could be had without swapping
	// SwapTotal and SwapFree are the swap `agentbox vm swap` gave it, and
	// what of it is free.
	SwapTotal, SwapFree int64
	// DiskSwapUsed is how much of the swap in use is on a disk swapfile
	// rather than zram (/proc/swaps).
	DiskSwapUsed int64
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
	// Refaults is the guest's workingset_refault_file since it booted
	// (/proc/vmstat): file pages read again soon after being dropped.
	// RefaultsKnown says the guest reported it.
	Refaults      uint64
	RefaultsKnown bool
	// Resident is what the VM holds of the host's memory (filled in by the
	// supervisor, from the host's side); 0 when unknown.
	Resident int64
	// HostTotal, HostAvailable and HostPressure are the host's MemTotal,
	// MemAvailable and PSI "some avg10" (filled in by the supervisor); 0
	// when unknown.
	HostTotal, HostAvailable int64
	HostPressure             float64
}

// HostReserve is what a host with total bytes of memory keeps for itself,
// its desktop and the app: hostload.Reserve, a fixed 4 GiB, what a desktop
// session with a browser and the app needs to stay responsive, or the host's
// hard floor where that is more (hostFloor, past 66 GiB). The VM's cap never
// takes more than the rest (MaxCap), on Linux and on a Mac alike, and the VM
// never grows past what the host can spare now (hostCeiling, HostKeep).
//
// It was a fifth, then a quarter (7.6 GiB of a 30.5 GiB host) after an
// agent building Rust froze a Linux PC whose VM had grown to its cap; but a
// share fixed in advance is the wrong guard for that: the cap is a ceiling
// the VM may reach on a day the user's own programs are small, and what
// keeps the desktop from stalling is the memory loop's reading of the host
// now (HostKeep, its memory pressure, hostCritical and the floor), which
// shrinks the VM within seconds whatever its cap (24 Nov 2026).
func HostReserve(total int64) int64 { return hostload.Reserve(total) }

// HostKeep is what a host with total bytes of memory keeps available while
// the VM runs: the VM grows only into what the host has beyond it
// (hostCeiling), and gives back while the host has less. The larger of
// memKeepMin and memKeepShare of it: 3 GiB on 16 GiB, 3.8 on 30.5, 8 on 64.
//
// It was HostReserve, a quarter, which is right for the cap, a ceiling the VM
// may reach on a day the user's own programs are small, but not as what the
// host must have free at every moment: on 19 Nov 2026 a 30.5 GiB host had
// 6.4 GiB available, short of the 7.6 it kept, so the VM couldn't grow past
// 10 GiB while its agents' builds filled its swap and stalled the daemon for
// minutes. What protects the desktop moment to moment is the host's own
// memory pressure, which holds growth and shrinks the VM within
// memShrinkAfterHost the moment anything on it stalls; this only has to
// cover what a desktop can take in those seconds (a browser opening a heavy
// page, an editor starting: one to two GiB) on top of its kernel's
// watermarks, and stay above the host's floor, where agents are stopped,
// with room for hostCritical between them.
func HostKeep(total int64) int64 {
	return max(memKeepMin, int64(float64(total)*memKeepShare))
}

// hostCriticalAt is the host's available memory under which the VM shrinks
// at once to its use: halfway from HostKeep down to the floor, where agents
// are stopped, so it always comes first.
func hostCriticalAt(total int64) int64 {
	return (HostKeep(total) + hostFloor(total)) / 2
}

// MaxCap is the most a VM's memory cap may be on a host with total bytes of
// memory: all of it less HostReserve, in whole GiB, and never less than
// DefaultMemoryMin; 0 when total is unknown.
func MaxCap(total int64) int64 {
	if total <= 0 {
		return 0
	}
	return max((total-HostReserve(total))/GiB*GiB, DefaultMemoryMin)
}

// FitHost lowers c's memory cap to MaxCap for a host with total bytes of
// memory, and reports whether it did: a cap from an earlier release, or set
// by hand, may leave the host less than its reserve. A Mac's VM (vz) too:
// its balloon gives memory back, but not before the Mac is short.
func FitHost(c Config, total int64) (Config, bool) {
	most := MaxCap(total)
	if most == 0 || c.MemoryCap <= most {
		return c, false
	}
	c.MemoryCap = max(most, c.MemoryMin)
	return c, true
}

// hostShort reports whether the host is short of memory: stalling on it, or
// with less than its reserve available.
func (s memSample) hostShort() bool {
	return s.HostTotal > 0 && (s.HostPressure > memHostPressure || s.HostAvailable < HostKeep(s.HostTotal))
}

// hostStalling reports whether the host stalls on memory (its PSI).
func (s memSample) hostStalling() bool {
	return s.HostTotal > 0 && s.HostPressure > memHostPressure
}

// belowReserve reports whether the host has less than it keeps available
// (HostKeep).
func (s memSample) belowReserve() bool {
	return s.HostTotal > 0 && s.HostAvailable < HostKeep(s.HostTotal)
}

// hostCritical reports whether the host is down to hostCriticalAt: next,
// its kernel kills something, the VM first.
func (s memSample) hostCritical() bool {
	return s.HostTotal > 0 && s.HostAvailable < hostCriticalAt(s.HostTotal)
}

// hostFloor is what a host with total bytes of memory can't go under
// without freezing: hostload.Floor, the larger of 2 GiB and 6% of it.
func hostFloor(total int64) int64 { return hostload.Floor(total) }

// belowFloor reports whether the host has less than its floor available.
func (s memSample) belowFloor() bool {
	return s.HostTotal > 0 && s.HostAvailable < hostFloor(s.HostTotal)
}

// hostCeiling is the most the VM may be given while the host keeps its
// reserve available: what it holds of the host's memory now (what's plugged,
// when that's unknown) and what the host has available beyond the reserve,
// in whole memGranules. What the guest touches of what it was given moves
// from the one to the other, so it holds however much of it the guest has
// touched. Meaningless when the host can't be read (HostTotal 0).
func (s memSample) hostCeiling() int64 {
	if s.HostTotal <= 0 {
		return 0
	}
	held := s.Resident
	if held <= 0 {
		held = s.Total
	}
	return max(held+s.HostAvailable-HostKeep(s.HostTotal), 0) / memGranule * memGranule
}

// Used is what the guest's programs use, without the page cache.
func (s memSample) Used() int64 { return s.Total - s.Available }

// memPolicy is the sizing rule, a pure function of what it's shown.
type memPolicy struct {
	Min, Cap int64
	// SafeUntil is when safe mode ends, after a crash loop (crashloop.go):
	// until then the VM keeps only memHeadroomMin beyond use, doesn't grow
	// ahead of rising use, and grows by memSafeGrowMax at a time.
	SafeUntil time.Time
}

// safe reports whether the VM is in safe mode at now.
func (p memPolicy) safe(now time.Time) bool { return now.Before(p.SafeUntil) }

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

	prevRefaults  uint64
	refaultsAt    time.Time // when prevRefaults was read; zero before the first
	refaultRate   float64   // bytes a second, smoothed
	floor         int64     // the cache floor refaults have shown it needs
	floorRaisedAt time.Time
	calmSince     time.Time     // since when refaults have been under memRefaultLow
	trimBackoff   time.Duration // the wait after the next too-low floor
	noTrimUntil   time.Time     // no trim before this: the floor was too low
	// movedAt is when the VM last stopped being settled: the first sample, a
	// resize, or refaults at memRefaultLow or more (settled).
	movedAt time.Time

	allIdleSince time.Time // since when every agent has been idle, by the daemon
	floorSince   time.Time // since when the host has been under its floor
	lastShed     time.Time // when an agent was last stopped for it
	// Ceiling is the most the VM may grow to now, by the last sample: the
	// cap, or less where the host can't spare that (hostCeiling); 0 when the
	// host can't be read. HoldBoots says the host is below its hard floor
	// now (hostFloor): no agent's machine should boot. Both are told to the
	// daemon (hostLoadLoop).
	Ceiling   int64
	HoldBoots bool
	// Wanted is what the VM would grow to now if the host could spare it,
	// when the host's memory is what holds it back (its ceiling, its
	// pressure, or less than it keeps available), by the last sample; 0
	// otherwise. The app says so: the user's computer is the limit, not the
	// VM's cap.
	Wanted int64
}

// observeHost keeps whether the host is under its floor, for booting and
// shedding.
func (st *memState) observeHost(s memSample, now time.Time) {
	st.HoldBoots = s.belowFloor()
	switch {
	case !s.belowFloor():
		st.floorSince = time.Time{}
	case st.floorSince.IsZero():
		st.floorSince = now
	}
}

// shed reports whether an agent should be stopped for the host at now: it
// has been under its floor for memEmergencyAfter, and none was within
// memShedEvery.
func (st *memState) shed(now time.Time) bool {
	if st.floorSince.IsZero() || now.Sub(st.floorSince) < memEmergencyAfter || !st.lastShed.IsZero() && now.Sub(st.lastShed) < memShedEvery {
		return false
	}
	st.lastShed = now
	return true
}

// idle reports whether every agent has been idle, by the daemon, for
// memIdleAfterAll.
func (st *memState) idle(now time.Time) bool {
	return !st.allIdleSince.IsZero() && now.Sub(st.allIdleSince) >= memIdleAfterAll
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
	// Shed asks for the agent that holds the most memory to be frozen and
	// stopped: the host has been under its floor for memEmergencyAfter.
	// ShedReason says so.
	Shed       bool
	ShedReason string
	Reason     string
}

// target is what the VM should have for s: what it uses, the file cache it
// keeps (keep, from floorFor), plus headroom, rounded up to memGranule,
// within Min and Cap. Use rising doesn't add to the headroom while the host
// has less than its reserve.
func (p memPolicy) target(s memSample, rate float64, keep int64) int64 {
	if s.belowReserve() {
		rate = 0
	}
	return p.clamp(s.Used() + keep + max(s.headroom(), int64(rate*memLookahead.Seconds())))
}

// busy is how many agents' machines are running with work under way.
func (s memSample) busy() int { return s.Agents - min(max(s.Idle, 0), s.Agents) }

// headroom is what the VM keeps beyond use for its agents to grow into: only
// memHeadroomMin while the host has less than its reserve.
func (s memSample) headroom() int64 {
	if s.belowReserve() {
		return memHeadroomMin
	}
	return max(memHeadroomMin, int64(s.busy())*memHeadroomPerAgent+int64(s.Agents-s.busy())*memHeadroomIdleAgent)
}

// cacheAllowance is the least file cache the VM keeps: what a build needs.
func (s memSample) cacheAllowance() int64 {
	return max(memCacheAllowanceMin, int64(float64(s.Used())*memCacheAllowanceShare))
}

// activeFile is the guest's active file pages: read more than once lately.
func (s memSample) activeFile() int64 { return max(s.FileCache-s.InactiveFile, 0) }

// cacheFloor is the file cache the VM never trims below: the allowance, its
// active file pages, or what refaults have shown it needs, whichever is
// largest, and never more than half the cap.
func (st *memState) cacheFloor(s memSample, p memPolicy) int64 {
	return min(max(s.cacheAllowance(), s.activeFile(), st.floor), max(p.Cap/2, s.cacheAllowance()))
}

// floorFor is the floor the VM keeps of its cache now: the cache floor, or
// only the allowance while the host has less than its reserve, refaults or
// not, and once every agent has been idle for a while. A host stalling with
// its reserve available is short of something a smaller cache won't give it.
func (st *memState) floorFor(s memSample, p memPolicy, now time.Time) int64 {
	if s.belowReserve() || st.idle(now) {
		return s.cacheAllowance()
	}
	return st.cacheFloor(s, p)
}

// keep is the cache the target keeps room for: what of the floor the guest
// caches.
func (st *memState) keep(s memSample, p memPolicy, now time.Time) int64 {
	return min(s.FileCache, st.floorFor(s, p, now))
}

// observeRefaults follows the guest's refault rate and moves the floor by it:
// raised, and trims held off, while the guest reads again what it dropped;
// lowered once that has stopped for memFloorCalm.
func (st *memState) observeRefaults(s memSample, p memPolicy, now time.Time) {
	if !s.RefaultsKnown {
		return
	}
	if !st.refaultsAt.IsZero() && s.Refaults >= st.prevRefaults {
		if dt := now.Sub(st.refaultsAt).Seconds(); dt > 0 {
			r := float64((s.Refaults-st.prevRefaults)*pageSize) / dt
			st.refaultRate = st.refaultRate*(1-memRefaultAlpha) + r*memRefaultAlpha
		}
	}
	st.prevRefaults, st.refaultsAt = s.Refaults, now
	if st.refaultRate >= memRefaultLow {
		st.movedAt = now
	}
	switch {
	case st.refaultRate > memRefaultHigh:
		st.calmSince = time.Time{}
		if now.Sub(st.floorRaisedAt) < memFloorRaiseEvery {
			return
		}
		st.floorRaisedAt = now
		want := s.FileCache + int64(st.refaultRate*memRefaultAhead.Seconds())
		st.floor = min(max(st.floor, want), max(p.Cap/2, s.cacheAllowance()))
		st.trimBackoff = min(max(st.trimBackoff*2, memTrimBackoff), memTrimBackoffMax)
		st.noTrimUntil = now.Add(st.trimBackoff)
	case st.refaultRate < memRefaultLow:
		if st.calmSince.IsZero() {
			st.calmSince = now
		} else if now.Sub(st.calmSince) >= memFloorCalm {
			st.floor -= st.floor / 8
			st.trimBackoff = 0
			st.calmSince = now
		}
	default:
		st.calmSince = time.Time{}
	}
}

// settled reports whether the VM has held still for memSettleAfter: not
// resized, and its guest not reading dropped pages again. Only a guest that
// reports its refaults can show that, so one that doesn't is never settled.
func (st *memState) settled(s memSample, now time.Time) bool {
	return s.RefaultsKnown && !st.movedAt.IsZero() && now.Sub(st.movedAt) >= memSettleAfter
}

// observe keeps what decide needs from one sample to the next: how fast use
// is rising, how busy the guest is, and since when no agent has run.
func (st *memState) observe(s memSample, now time.Time) {
	if st.movedAt.IsZero() {
		st.movedAt = now
	}
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

	// Idle is only known from the daemon: 0 counts every agent as busy.
	switch {
	case s.Agents > 0 && s.busy() > 0:
		st.allIdleSince = time.Time{}
	case st.allIdleSince.IsZero():
		st.allIdleSince = now
	}
}

// quiet reports whether the VM has stopped working: no agent running for a
// while, every agent idle by the daemon for memIdleAfterAll, or, when the
// host is short, its CPUs all but idle for longer. Agents waiting on their
// model are idle on the CPU, and read everything again on their next turn, so
// CPUs alone aren't enough while the host can spare the memory.
func (st *memState) quiet(now time.Time, hostShort bool) bool {
	return !st.noAgentsSince.IsZero() && now.Sub(st.noAgentsSince) >= memNoAgentsAfter ||
		st.idle(now) ||
		hostShort && !st.idleSince.IsZero() && now.Sub(st.idleSince) >= memIdleAfter
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
	st.observeRefaults(*s, p, now)
	st.observeHost(*s, now)
	safe := p.safe(now)
	rate := st.rate
	if safe {
		rate = 0
	}
	keep := st.keep(*s, p, now)
	want := p.target(*s, rate, keep)
	reason := fmt.Sprintf("uses %s with %d agents running, %d busy", gib(s.Used()), s.Agents, s.busy())
	if rate*memLookahead.Seconds() > float64(s.headroom()) {
		reason = fmt.Sprintf("uses %s, rising %s a second", gib(s.Used()), gib(int64(rate)))
	}
	// lean is the least the VM is shrunk to for the host: its use, the cache
	// allowance and memHeadroomMin; its use and memHeadroomLeast once the
	// host is down to half its reserve.
	lean := p.clamp(s.Used() + min(s.FileCache, s.cacheAllowance()) + memHeadroomMin)
	if safe && lean < want {
		want = lean
	}
	if s.Pressure > memPressureSome && s.Available < memHeadroomMin && now.Sub(st.lastGrow) >= memGrowCooldown {
		if bumped := p.clamp(st.Requested + memPressureStep); bumped > want {
			want = bumped
			reason = fmt.Sprintf("stalled on memory %.0f%% of the last 10s", s.Pressure)
		}
	}
	need := want
	critical := s.hostCritical()
	if critical {
		lean = p.clamp(s.Used() + memHeadroomLeast)
		want = min(want, lean)
	}
	// The cap is a ceiling, not a target: the VM never has more than the
	// host can spare now. Past it, growing stops, and a VM already past it
	// is shrunk back, never below lean (nor grown to it).
	st.Ceiling = 0
	if s.HostTotal > 0 {
		ceiling := s.hostCeiling()
		st.Ceiling = min(p.Cap, max(ceiling, p.Min))
		if want > ceiling {
			want = max(ceiling, min(lean, st.Requested))
			reason = fmt.Sprintf("this machine has %s available and keeps %s for itself", gib(s.HostAvailable), gib(HostKeep(s.HostTotal)))
		}
	}
	if want > st.Requested {
		want, reason = st.limitGrowth(*s, p, now, want, reason)
	}
	st.Wanted = 0
	if need >= max(want, st.Requested)+memGranule && s.hostHolds(need) {
		st.Wanted = need
	}
	shrinkMin, shrinkAfter := memShrinkMin, memShrinkAfter
	switch {
	case critical:
		shrinkMin, shrinkAfter = memGranule, 0
		reason += fmt.Sprintf(", and this machine has only %s available of the %s it keeps", gib(s.HostAvailable), gib(HostKeep(s.HostTotal)))
	case s.belowReserve():
		shrinkMin, shrinkAfter = memGranule, memShrinkAfterHost
		reason += fmt.Sprintf(", and this machine has %s available of the %s it keeps", gib(s.HostAvailable), gib(HostKeep(s.HostTotal)))
	case s.hostStalling():
		shrinkMin, shrinkAfter = memGranule, memShrinkAfterHost
		reason += fmt.Sprintf(", and this machine stalled on memory %.0f%% of the last 10s", s.HostPressure)
	case s.HostTotal > 0 && st.Requested > s.hostCeiling()+memGranule:
		// Not short yet, but it would be once the guest touched what it
		// has. Within a granule of it, it waits for a whole memShrinkMin.
		shrinkMin, shrinkAfter = memGranule, memShrinkAfterHost
	}
	if safe {
		reason += ", in safe mode after a crash loop"
	}
	var d memDecision
	switch {
	case want > st.Requested:
		st.Requested, st.lastGrow = want, now
		st.lowSince, st.lowMax = time.Time{}, 0
		d = memDecision{Target: want, Reason: reason}
	case want <= st.Requested-shrinkMin:
		if st.lowSince.IsZero() {
			st.lowSince, st.lowMax = now, want
		}
		st.lowMax = max(st.lowMax, want)
		if now.Sub(st.lowSince) >= shrinkAfter {
			d = memDecision{Target: st.lowMax, Shrink: true, Reason: reason + fmt.Sprintf(" for %s", shrinkAfter)}
			st.Requested = st.lowMax
			st.lowSince, st.lowMax = time.Time{}, 0
		}
	default:
		st.lowSince, st.lowMax = time.Time{}, 0
	}
	if d.Target != 0 {
		st.movedAt = now
	}
	switch {
	case critical && (d.Shrink || d.Target == 0 && s.Resident-s.Used() > memReclaimMin) && now.Sub(st.lastReclaim) >= memTrimEvery:
		// Down to half its reserve: every page of cache goes, so the
		// blocks unplug and free page reporting returns the rest.
		d.Reclaim, d.HostShort, st.lastReclaim = true, true, now
		if d.Target == 0 {
			d.Reason = reason
		}
	case d.Shrink:
		// What's in the blocks it unplugs moves to the rest, which the target
		// left room in for the floor: only the cache beyond it goes.
		if n := min(s.InactiveFile, s.FileCache-st.floorFor(*s, p, now), s.Total-d.Target); n >= memTrimMin {
			d.Trim, d.HostShort, st.lastTrim = n, s.hostShort(), now
		}
	case d.Target == 0 && s.Resident > 0 && s.Resident-s.Used() > memReclaimMin &&
		st.quiet(now, s.hostShort()) && now.Sub(st.lastReclaim) >= memReclaimEvery:
		d.Reclaim, st.lastReclaim = true, now
		d.Reason = fmt.Sprintf("quiet, and holding %s of this machine's memory for %s in use", gib(s.Resident), gib(s.Used()))
	case d.Target == 0 && s.Resident > 0:
		d.Trim, d.HostShort, d.Reason = st.trim(*s, p, now)
	}
	if st.shed(now) {
		d.Shed = true
		d.ShedReason = fmt.Sprintf("this machine has had only %s available for %s, under the %s it can't go below", gib(s.HostAvailable), now.Sub(st.floorSince).Round(time.Second), gib(hostFloor(s.HostTotal)))
	}
	return d
}

// limitGrowth holds back or slows a grow from st.Requested to want, and says
// why: none while the host stalls on memory or has less than it keeps, by
// this sample; none within memGrowEvery of the last; and at most memGrowMax
// at a time (memSafeGrowMax in safe mode). Never for the host having been
// short a moment ago, nor for it being busy (24 Nov 2026).
func (st *memState) limitGrowth(s memSample, p memPolicy, now time.Time, want int64, reason string) (int64, string) {
	switch {
	case s.hostStalling():
		return st.Requested, fmt.Sprintf("this machine stalled on memory %.0f%% of the last 10s", s.HostPressure)
	case s.belowReserve():
		return st.Requested, fmt.Sprintf("this machine has %s available of the %s it keeps", gib(s.HostAvailable), gib(HostKeep(s.HostTotal)))
	case !st.lastGrow.IsZero() && now.Sub(st.lastGrow) < memGrowEvery:
		return st.Requested, reason
	}
	step := int64(memGrowMax)
	if p.safe(now) {
		step = memSafeGrowMax
	}
	return min(want, st.Requested+step), reason
}

// hostHolds reports whether the host's memory is what keeps the VM from
// growing to need now: past what the host can spare, or the host short or
// stalling.
func (s memSample) hostHolds(need int64) bool {
	if s.HostTotal <= 0 {
		return false
	}
	return need > s.hostCeiling() || s.hostShort()
}

// trim is how much of the guest's inactive file cache to reclaim while it's
// busy, and why, and whether the host was short: never past the floor
// (floorFor), never while refaults have held trims off, and never more than
// memTrimMax at once. With the host short of memory, every memTrimEvery; with
// the host below its reserve, the shrink to lean gives back what refaults keep
// trims off. With the host not short, only once the VM has settled (settled),
// and then at most every memSettledTrimEvery.
func (st *memState) trim(s memSample, p memPolicy, now time.Time) (int64, bool, string) {
	short := s.hostShort()
	every := memTrimEvery
	if !short {
		every = memSettledTrimEvery
		if !st.settled(s, now) {
			return 0, false, ""
		}
	}
	if now.Before(st.noTrimUntil) {
		return 0, false, ""
	}
	floor := st.floorFor(s, p, now)
	n := min(s.InactiveFile, s.FileCache-floor, memTrimMax)
	if n < memTrimMin || now.Sub(st.lastTrim) < every || now.Sub(st.lastReclaim) < memTrimEvery {
		return 0, false, ""
	}
	st.lastTrim = now
	reason := fmt.Sprintf("the VM has been settled for %s, and caches %s of files, keeping %s", now.Sub(st.movedAt).Round(time.Minute), gib(s.FileCache), gib(floor))
	switch {
	case !short:
	case s.HostPressure > memHostPressure:
		reason = fmt.Sprintf("this machine stalled on memory %.0f%% of the last 10s, and the VM caches %s of files, keeping %s", s.HostPressure, gib(s.FileCache), gib(floor))
	default:
		reason = fmt.Sprintf("this machine has %s available, and the VM caches %s of files, keeping %s", gib(s.HostAvailable), gib(s.FileCache), gib(floor))
	}
	return n, short, reason
}

func gib(n int64) string { return fmt.Sprintf("%.1f GiB", float64(n)/float64(GiB)) }

// memScript prints what parseMemSample reads, in one ssh round trip. An
// agent's machine is a cgroup of its own while it runs: lxc.payload.<name>,
// at the root or in agentbox.agents, or agentbox/<name> where an earlier release's shared budget started it,
// beside its .monitor.
const memScript = `cat /proc/meminfo /proc/pressure/memory 2>/dev/null
head -n1 /proc/stat
grep -E '^workingset_refault(_file)? ' /proc/vmstat 2>/dev/null
echo "disk_swap_used $(awk 'NR>1 && $1 !~ /^\/dev\/zram/ {u+=$4} END {print u+0}' /proc/swaps 2>/dev/null)"
echo "auto_online $(cat /sys/devices/system/memory/auto_online_blocks 2>/dev/null)"
echo "agents $(ls -d /sys/fs/cgroup/lxc.payload.* /sys/fs/cgroup/agentbox.agents/lxc.payload.* /sys/fs/cgroup/agentbox/*/ 2>/dev/null | grep -vc '\.monitor/*$')"`

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
		case "disk_swap_used":
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			s.DiskSwapUsed = kb << 10
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
		case "workingset_refault_file", "workingset_refault":
			// Kernels before 5.9 count file and anon refaults together.
			if n, err := strconv.ParseUint(f[1], 10, 64); err == nil && (f[0] == "workingset_refault_file" || !s.RefaultsKnown) {
				s.Refaults, s.RefaultsKnown = n, true
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
