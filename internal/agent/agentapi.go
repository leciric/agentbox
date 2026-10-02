package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// recheckTimeout bounds goRecheckAgentAPI's wait for a freshly started
// agent's boot: longer than replugHiddenSocket's own 120s systemctl wait, so
// that timeout is what gives up first.
const recheckTimeout = 3 * time.Minute

// The in-agent API: an Incus proxy device exposes, inside the agent, a host
// socket that belongs to that agent alone. The daemon knows who is calling by
// the socket a request arrives on, so an agent can never act on another one.
const agentAPIDevice = "agentbox"

// AgentBinaryPath is where EnsureAgentAPI pushes the agentbox binary inside an
// agent. It is on the agent's PATH, and it is also what the desktop MCP server
// is started from (D64).
const AgentBinaryPath = "/usr/local/bin/agentbox"

// EnsureAgentAPI wires a running agent to the daemon: the proxy device for its
// in-agent API socket and a current copy of the agentbox binary.
func (m *Manager) EnsureAgentAPI(ctx context.Context, a state.Agent) error {
	if m.AgentSocket == nil {
		return nil
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	if _, ok := devices[agentAPIDevice]; ok {
		if err := m.replugHiddenSocket(ctx, a, false); err != nil {
			return err
		}
	} else if err := m.addAgentAPIDevice(ctx, a); err != nil {
		return err
	}
	if m.Binary != "" {
		return m.pushBinary(ctx, a.Instance)
	}
	return nil
}

func (m *Manager) addAgentAPIDevice(ctx context.Context, a state.Agent) error {
	return m.Incus.AddDevice(ctx, a.Instance, agentAPIDevice,
		"proxy",
		"connect=unix:"+m.AgentSocket(a.Instance),
		"listen=unix:"+api.InAgentSocket,
		"bind=instance",
		fmt.Sprintf("uid=%d", m.User.UID),
		fmt.Sprintf("gid=%d", m.User.GID),
		"mode=0660")
}

// RestoreAgentAPISocket waits for a running agent to finish booting, and
// plugs its in-agent API device in again if its socket is hidden. It's for
// agents Incus started itself, which the daemon finds running when it starts:
// Incus starts again, with the VM or the machine, the agents that ran.
func (m *Manager) RestoreAgentAPISocket(ctx context.Context, a state.Agent) error {
	if m.AgentSocket == nil {
		return nil
	}
	return m.replugHiddenSocket(ctx, a, true)
}

// replugHiddenSocket plugs the in-agent API device in again when its socket
// isn't in the agent. Incus starts the device's listener as the agent starts,
// before its systemd mounts a tmpfs over /run, which hides the socket: every
// agent started again has none (a new one gets its device after its boot).
// Plugging it in again, once booted, puts it back. With boot set, it waits
// for the boot first, and an agent it can't ask is an error, for the caller
// to ask again; without, an agent it can't ask (stopped, say) is left as it is.
func (m *Manager) replugHiddenSocket(ctx context.Context, a state.Agent, boot bool) error {
	check := "test -S " + api.InAgentSocket + " && echo there || echo missing"
	if boot {
		check = "timeout 120 systemctl is-system-running --wait >/dev/null 2>&1; " + check
	}
	out, err := m.Incus.Exec(ctx, a.Instance, "sh", "-c", check)
	if err != nil && boot {
		// A running agent that can't be asked is asked again (the daemon's
		// incuswatch.go): Incus may not be answering yet.
		return err
	}
	if err != nil || strings.TrimSpace(out) != "missing" {
		return nil
	}
	m.logf("%s's in-agent API socket is hidden under its /run: plugging it in again", a.Ref())
	if err := m.Incus.RemoveDevice(ctx, a.Instance, agentAPIDevice); err != nil {
		return err
	}
	return m.addAgentAPIDevice(ctx, a)
}

// goRecheckAgentAPI waits, once an agent Start just booted has finished
// coming up, and plugs its in-agent API socket in again if its systemd
// mounted a tmpfs over /run after EnsureAgentAPI's immediate check found the
// socket there (replugHiddenSocket's race). Start doesn't wait for this
// itself: a cold boot can take a while, and the caller only needs the
// instance back.
func (m *Manager) goRecheckAgentAPI(ctx context.Context, a state.Agent) {
	run := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recheckTimeout)
		defer cancel()
		if err := m.RestoreAgentAPISocket(ctx, a); err != nil {
			m.logf("in-agent API socket for %s: %v", a.Ref(), err)
		}
	}
	if m.RecheckAgentAPI != nil {
		m.RecheckAgentAPI(run)
		return
	}
	go run()
}

// pushBinary replaces the agent's agentbox binary with the daemon's. `incus
// file push` opens its target for writing, which a running agent refuses with
// "text file busy": the agent's MCP servers run from that binary. So it pushes
// beside it and renames it into place, which leaves the running processes on
// the old file and starts every new one on the new.
func (m *Manager) pushBinary(ctx context.Context, instance string) error {
	next := AgentBinaryPath + ".new"
	if err := m.Incus.PushFile(ctx, m.Binary, instance, next, 0o755); err != nil {
		return err
	}
	if _, err := m.Incus.Exec(ctx, instance, "mv", "-f", next, AgentBinaryPath); err != nil {
		_, _ = m.Incus.Exec(context.WithoutCancel(ctx), instance, "rm", "-f", next)
		return err
	}
	return nil
}
