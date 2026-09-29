package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"agentbox/internal/state"
)

// Connectors are remote MCP servers the daemon signs in to and relays to
// (internal/connectors). Inside the agent each is one more MCP server in every
// AI tool's configuration, `agentbox connector mcp <name>`, which holds no
// token: the configuration is all an agent gets of it.

// connectorNames are the enabled connectors an agent is given, by name. A
// lead has none: it runs on the host, with no machine to relay from.
func (m *Manager) connectorNames(ctx context.Context, a state.Agent) ([]string, error) {
	if a.IsLead() {
		return nil, nil
	}
	found, err := m.Store.AgentConnectors(ctx, a.Project, a.Name)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, c := range found {
		if c.Enabled {
			names = append(names, c.Name)
		}
	}
	return names, nil
}

// SyncConnectors rewrites the MCP servers of every running agent a change to
// connectors reaches — all of a project's when agent is "", one otherwise — so
// that the next session of its AI tool has them. A session already running
// keeps what it started with, as every AI tool reads its MCP servers when it
// starts; Claude Code's /mcp reconnects them.
func (m *Manager) SyncConnectors(ctx context.Context, project, agent string) error {
	var agents []state.Agent
	if agent == "" {
		all, err := m.Store.Agents(ctx, project)
		if err != nil {
			return err
		}
		agents = all
	} else {
		a, err := m.Store.Agent(ctx, project, agent)
		if err != nil {
			return err
		}
		agents = []state.Agent{a}
	}
	var failed []string
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady {
			continue // one being made gets them from configure
		}
		if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
			continue
		}
		if err := m.syncConnectors(ctx, a); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", a.Ref(), err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't give the connectors to %s: they get them when they are next set up", strings.Join(failed, ", "))
	}
	return nil
}

// syncConnectors writes one agent's MCP servers into all three AI tools'
// configuration: Codex's and OpenCode's whole, as PrepareChatModel does, and
// Claude Code's mcpServers merged into the ~/.claude.json it keeps its own
// state in.
func (m *Manager) syncConnectors(ctx context.Context, a state.Agent) error {
	window, err := m.agentCompactWindow(ctx, a)
	if err != nil {
		return err
	}
	if err := m.prepareAgentCodexSettings(ctx, a, window); err != nil {
		return err
	}
	if err := m.prepareAgentOpenCodeSettings(ctx, a); err != nil {
		return err
	}
	home := "/home/" + m.User.Name
	names, err := m.connectorNames(ctx, a)
	if err != nil {
		return err
	}
	path := home + "/.claude.json"
	var existing bytes.Buffer
	_ = m.Incus.UserExec(ctx, a.Instance, m.User.Name, "cat "+shellQuote(path)+" 2>/dev/null", nil, &existing, io.Discard)
	merged, err := withMCPServers(existing.Bytes(), claudeMCPServers(agentMCPServers(home, names)))
	if err != nil {
		return err
	}
	return m.Incus.WriteFile(ctx, a.Instance, path, merged, m.User.UID, m.User.GID, 0o600)
}

// withMCPServers replaces the mcpServers of a ~/.claude.json and leaves every
// other key as it was. A file that isn't a JSON object is refused rather than
// overwritten: it holds Claude Code's own state.
func withMCPServers(existing []byte, servers map[string]any) ([]byte, error) {
	config := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &config); err != nil {
			return nil, errors.New("~/.claude.json isn't a JSON object, and is left as it is")
		}
	}
	raw, err := json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	config["mcpServers"] = raw
	return json.Marshal(config)
}
