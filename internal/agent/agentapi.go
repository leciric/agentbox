package agent

import (
	"context"
	"fmt"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

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
	if _, ok := devices[agentAPIDevice]; !ok {
		if _, err := m.Incus.Run(ctx, "config", "device", "add", a.Instance, agentAPIDevice, "proxy",
			"connect=unix:"+m.AgentSocket(a.Instance),
			"listen=unix:"+api.InAgentSocket,
			"bind=instance",
			fmt.Sprintf("uid=%d", m.User.UID),
			fmt.Sprintf("gid=%d", m.User.GID),
			"mode=0660"); err != nil {
			return err
		}
	}
	if m.Binary != "" {
		return m.pushBinary(ctx, a.Instance)
	}
	return nil
}

// pushBinary replaces the agent's agentbox binary with the daemon's. `incus
// file push` opens its target for writing, which a running agent refuses with
// "text file busy": the agent's MCP servers run from that binary. So it pushes
// beside it and renames it into place, which leaves the running processes on
// the old file and starts every new one on the new.
func (m *Manager) pushBinary(ctx context.Context, instance string) error {
	next := AgentBinaryPath + ".new"
	if _, err := m.Incus.Run(ctx, "file", "push", m.Binary, instance+next, "--mode", "0755"); err != nil {
		return err
	}
	if _, err := m.Incus.Run(ctx, "exec", instance, "--", "mv", "-f", next, AgentBinaryPath); err != nil {
		_, _ = m.Incus.Run(context.WithoutCancel(ctx), "exec", instance, "--", "rm", "-f", next)
		return err
	}
	return nil
}
