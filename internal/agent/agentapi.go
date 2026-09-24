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
// is started from ([D64](../../docs/implementation/decisions.md#d64)).
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
		if _, err := m.Incus.Run(ctx, "file", "push", m.Binary, a.Instance+AgentBinaryPath, "--mode", "0755"); err != nil {
			return err
		}
	}
	return nil
}
