package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentbox/internal/state"
)

// An agent's run phase (internal/runslots, internal/daemon/runslots.go) is
// what it runs while it holds a run slot: the app, docker compose,
// Playwright, its browser and emulator, its recordings. What it runs through
// AgentBox (`agentbox run up -- <cmd>`, a shimmed dev server, `agentbox
// heavy`) joins a cgroup of its own, under the machine's, like a heavy
// command's (PlaceRun); ending the run phase kills what is left in it and
// stops the containers that weren't running when the slot was granted.
//
// In its code phase an agent's machine has a memory.high (CodePhaseHigh)
// under its memory.max: a dev server left running after its run phase ended
// is squeezed (the kernel reclaims from it and slows what allocates) rather
// than holding memory the runs are counted against. A build or test past it
// slows rather than being killed, and only the machine's own memory.max
// kills.

// CodePhaseHigh is an agent's machine's memory.high while it holds no run
// slot: three quarters of its memory.max, 0 (none) when it has none.
func CodePhaseHigh(limit int64) int64 {
	if limit <= 0 {
		return 0
	}
	return limit / 4 * 3
}

// SetMemoryHigh sets an agent's machine's memory.high to high (0 for none,
// "max"), writing only when it changes, and nothing for a machine whose
// cgroup isn't there.
func SetMemoryHigh(instance string, high int64) error {
	return setCgroupLimit(agentCgroup(cgroupRoot, instance), "memory.high", high, writeCgroup)
}

// MachineUse is what an agent's machine uses now, as the app counts it:
// memory.current less its page cache (agentMemory). ok is false when its
// cgroup can't be read: stopped, or not on cgroup2.
func MachineUse(instance string) (int64, bool) {
	used := agentMemory(cgroupRoot, instance, -1)
	return max(used, 0), used >= 0
}

// KillRun kills every process left in a run's cgroup (cgroup.kill) and
// removes it once they are gone. A run with no cgroup has nothing to kill.
func KillRun(instance, key string) error {
	return killRun(cgroupRoot, instance, key, writeCgroup, time.Sleep)
}

func killRun(root, instance, key string, write func(path, value string) error, sleep func(time.Duration)) error {
	dir := runCgroup(root, instance, key)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// A frozen process can't die until it is thawed.
	if err := freezeRun(root, instance, key, false, write); err != nil {
		return err
	}
	if runPopulated(root, instance, key) {
		if err := write(filepath.Join(dir, "cgroup.kill"), "1"); err != nil {
			return fmt.Errorf("killing what %s ran: %w", key, err)
		}
		for i := 0; i < 50 && runPopulated(root, instance, key); i++ {
			sleep(100 * time.Millisecond)
		}
	}
	if runPopulated(root, instance, key) {
		return fmt.Errorf("what %s ran is still dying", key)
	}
	return removeCgroup(dir)
}

// containerID is a container's full ID as `docker ps --no-trunc` prints it.
var containerID = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// RunningContainers is the containers running in a's machine, by full ID:
// none when Docker isn't installed or its daemon isn't running, which it
// asks systemd rather than docker, so a socket-activated daemon isn't started
// to answer.
func (m *Manager) RunningContainers(ctx context.Context, a state.Agent) ([]string, error) {
	out, err := m.Incus.Exec(ctx, a.Instance, "bash", "-c",
		`if command -v docker >/dev/null 2>&1 && systemctl is-active --quiet docker.service; then docker ps -q --no-trunc; fi`)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	return containerIDs(out), nil
}

func containerIDs(out string) []string {
	var ids []string
	for _, f := range strings.Fields(out) {
		if containerID.MatchString(f) {
			ids = append(ids, f)
		}
	}
	return ids
}

// StopContainersExcept stops every container running in a's machine but
// those in keep, giving each ten seconds to end, and answers which it
// stopped.
func (m *Manager) StopContainersExcept(ctx context.Context, a state.Agent, keep []string) ([]string, error) {
	running, err := m.RunningContainers(ctx, a)
	if err != nil {
		return nil, err
	}
	stop := containersToStop(running, keep)
	if len(stop) == 0 {
		return nil, nil
	}
	args := append([]string{"docker", "stop", "-t", "10"}, stop...)
	if out, err := m.Incus.Exec(ctx, a.Instance, args...); err != nil {
		return nil, fmt.Errorf("stopping the run phase's containers: %w: %s", err, strings.TrimSpace(out))
	}
	return stop, nil
}

// containersToStop is running less keep.
func containersToStop(running, keep []string) []string {
	kept := map[string]bool{}
	for _, id := range keep {
		kept[id] = true
	}
	var stop []string
	for _, id := range running {
		if !kept[id] {
			stop = append(stop, id)
		}
	}
	return stop
}
