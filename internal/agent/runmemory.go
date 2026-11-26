package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RunMemory is what a heavy command's cgroup (RunCgroup) holds now (used)
// and held at most (peak), without the page cache and the kernel's
// reclaimable caches, as agentMemory counts an agent: they are free to the
// VM's MemAvailable too, which MemoryWorkers splits. memory.peak has no such
// split, so the caches still held as it is read are taken off it: as a run
// ends its processes are gone and what is left is mostly the cache they
// filled, which only grows until the kernel takes it back, so peak errs high
// (and high is the side MemoryWorkers errs on). ok is false when the cgroup
// can't be read: gone, or a kernel without memory.peak (before 5.19) for
// peak, which is then 0.
func RunMemory(instance, key string) (used, peak int64, ok bool) {
	return runMemory(runCgroup(cgroupRoot, instance, key))
}

func runMemory(dir string) (used, peak int64, ok bool) {
	current, err := readCgroupInt(filepath.Join(dir, "memory.current"))
	if err != nil {
		return 0, 0, false
	}
	stat, err := os.ReadFile(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return 0, 0, false
	}
	caches := statValue(string(stat), "active_file") + statValue(string(stat), "inactive_file") + statValue(string(stat), "slab_reclaimable")
	used = max(current-caches, 0)
	if p, err := readCgroupInt(filepath.Join(dir, "memory.peak")); err == nil {
		peak = max(p-caches, used)
	}
	return used, peak, true
}

// AgentMemoryRoom is what an agent's machine may still take before its own
// memory.max (MemoryLimit), past which the kernel kills in it: the limit less
// what it holds without its caches (agentMemory). ok is false when it has no
// limit, or it can't be read.
func AgentMemoryRoom(instance string) (int64, bool) {
	return agentMemoryRoom(cgroupRoot, instance)
}

func agentMemoryRoom(root, instance string) (int64, bool) {
	limit, err := readCgroupInt(filepath.Join(agentCgroup(root, instance), "memory.max"))
	if err != nil || limit <= 0 {
		return 0, false
	}
	used := agentMemory(root, instance, -1)
	if used < 0 {
		return 0, false
	}
	return max(limit-used, 0), true
}

// readCgroupInt reads a cgroup file holding one number; "max" is an error.
func readCgroupInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}
