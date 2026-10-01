package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"agentbox/internal/datamove"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// worktreeDevice is the disk device an agent's worktree is mounted with, at
// the same path as on the host (create).
const worktreeDevice = "worktree"

// MoveDevices points a's machine at where AgentBox's data moved (package
// datamove): its worktree's device at a.Worktree, which state.db already has
// moved, and every other device's paths (the sockets the daemon and the
// browser are reached on) at where k moved them. A machine whose worktree
// moved is stopped around the change, since its worktree moves inside it too,
// and started again if it ran, or was to run: one Incus started with the VM
// failed to, its worktree gone from the old path. Claude Code's sessions in
// it follow the worktree, so its chat carries on: a stopped machine is started
// for that, and stopped again.
func (m *Manager) MoveDevices(ctx context.Context, a state.Agent, k datamove.Marker) error {
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if errors.Is(err, incus.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	changed := movedDevices(devices, a.Worktree, k)
	if len(changed) == 0 {
		return nil
	}
	_, worktreeMoved := changed[worktreeDevice]
	running := inst.Status == "Running"
	restart := worktreeMoved && (running || inst.Config["volatile.last_state.power"] == "RUNNING")
	if worktreeMoved && running {
		if err := m.Incus.Stop(ctx, a.Instance); err != nil {
			return err
		}
	}
	for name, dev := range changed {
		if err := m.Incus.RemoveDevice(ctx, a.Instance, name); err != nil {
			return fmt.Errorf("moving %s's device %s: %w", a.Instance, name, err)
		}
		var options []string
		for key, value := range dev {
			if key != "type" {
				options = append(options, key+"="+value)
			}
		}
		if err := m.Incus.AddDevice(ctx, a.Instance, name, dev["type"], options...); err != nil {
			return fmt.Errorf("moving %s's device %s: %w", a.Instance, name, err)
		}
	}
	if !worktreeMoved {
		return nil
	}
	// Claude Code's sessions are renamed inside the machine, which has to run
	// for that: one that was stopped is started for it, and stopped again.
	if err := m.Incus.Start(ctx, a.Instance); err != nil {
		return err
	}
	if !restart {
		defer func() {
			if err := m.Incus.Stop(ctx, a.Instance); err != nil {
				m.logf("Stopping %s again after moving it: %v", a.Instance, err)
			}
		}()
	}
	old := devices[worktreeDevice]["path"]
	if old == "" || old == a.Worktree {
		return nil
	}
	from, to := datamove.ClaudeProjectName(old), datamove.ClaudeProjectName(a.Worktree)
	script := `cd "$HOME/.claude/projects" 2>/dev/null || exit 0
for d in ` + from + ` ` + from + `-*; do
	[ -d "$d" ] || continue
	n=` + to + `${d#` + from + `}
	[ -e "$n" ] || mv -- "$d" "$n"
done`
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, script, nil, nil, nil); err != nil {
		m.logf("Moving Claude Code's sessions in %s to %s: %v", a.Instance, a.Worktree, err)
	}
	return nil
}

// movedDevices are the devices that change for the move k, each in full: the
// worktree's disk at worktree, and any other device whose paths moved.
func movedDevices(devices map[string]map[string]string, worktree string, k datamove.Marker) map[string]map[string]string {
	changed := map[string]map[string]string{}
	for name, dev := range devices {
		moved := maps.Clone(dev)
		if name == worktreeDevice && dev["type"] == "disk" {
			if worktree != "" {
				moved["source"], moved["path"] = worktree, worktree
			}
		} else {
			for _, key := range []string{"source", "path", "connect", "listen"} {
				v, ok := dev[key]
				if !ok {
					continue
				}
				scheme := ""
				if s, rest, ok := strings.Cut(v, ":"); ok && (s == "unix" || s == "tcp") {
					scheme, v = s+":", rest
				}
				moved[key] = scheme + k.Rewrite(v)
			}
		}
		if !maps.Equal(moved, dev) {
			changed[name] = moved
		}
	}
	return changed
}
