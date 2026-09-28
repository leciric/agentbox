package agent

import (
	"os"
	"path/filepath"
	"strings"

	"agentbox/internal/hostsetup"
)

// The shared budget's reserve: the host's memory less what agents may use,
// kept for the host's own apps as memory.low on systemd's user.slice and
// system.slice. Below memory.low, the kernel reclaims a cgroup's memory only
// once there's nothing left to take from anyone unprotected — here the
// agents, whose cgroup has none — so under pressure the agents give memory
// back first, and until then they may use it all.
//
// Nearly all of it goes to user.slice: the desktop, the browser, the
// AgentBox app and the audio server are there, and they are what froze.
// system.slice gets a quarter, at most 2 GiB, for the journal, Incus and
// Docker — Incus' own daemon stalling stalls every agent operation.
//
// Protection only reaches the processes inside the slices, deep in
// user@UID.service and its apps, with the cgroup2 mount's
// memory_recursiveprot: without it, a child without a memory.low of its own
// gets none of its parent's. systemd mounts it that way since v247, and
// ReserveReachesApps checks.
//
// systemd writes memory.low itself when it reloads a unit's cgroup, which puts
// the slices' back to 0; the daemon writes the budget every ten seconds while
// it's on, which puts the reserve back.

// reserveSlices are the slices the reserve is set on.
var reserveSlices = hostsetup.ReserveSlices

// reservePath is a slice's memory.low: next to the budget's cgroup, at the
// cgroup root.
func reservePath(slice string) string {
	return filepath.Join(filepath.Dir(BudgetDir), slice, "memory.low")
}

func reservePaths() []string {
	var out []string
	for _, slice := range reserveSlices {
		out = append(out, reservePath(slice))
	}
	return out
}

// reserveSplit shares the reserve between the slices: system.slice a quarter,
// at most 2 GiB, and user.slice the rest.
func reserveSplit(reserve int64) map[string]int64 {
	system := min(reserve/4, int64(2)<<30)
	return map[string]int64{"user.slice": reserve - system, "system.slice": system}
}

// mountInfo is where ReserveReachesApps reads the cgroup2 mount from. A
// variable for tests.
var mountInfo = "/proc/self/mountinfo"

// ReserveReachesApps says whether the cgroup root is mounted with
// memory_recursiveprot, without which the slices' memory.low protects none of
// the apps in them. true when it can't be told.
func ReserveReachesApps() bool {
	b, err := os.ReadFile(mountInfo)
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(b), "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		fields, rest := strings.Fields(pre), strings.Fields(post)
		if !ok || len(fields) < 5 || len(rest) < 3 || fields[4] != cgroupRoot || rest[0] != "cgroup2" {
			continue
		}
		for _, opt := range strings.Split(rest[2], ",") {
			if opt == "memory_recursiveprot" {
				return true
			}
		}
		return false
	}
	return true
}
