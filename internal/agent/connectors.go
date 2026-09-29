package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"agentbox/internal/state"
)

// Connectors are remote MCP servers the daemon signs in to and relays to
// (internal/connectors). Inside the agent each is one more MCP server in every
// AI tool's configuration, `agentbox connector mcp <name>`, which holds no
// token: the configuration is all an agent gets of it.

// connectorNames are the enabled connectors an agent is given, by name: its
// project's that its limit lets through, and its own. A lead is given its
// project's, relayed through its own socket rather than a machine's
// (leadMCPServers).
func (m *Manager) connectorNames(ctx context.Context, a state.Agent) ([]string, error) {
	var found []state.Connector
	var err error
	if a.IsLead() {
		found, err = m.Store.Connectors(ctx, a.Project, "")
	} else {
		found, err = m.Store.AgentConnectors(ctx, a.Project, a.Name)
	}
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

// CheckConnectorLimit refuses a limit (CreateOptions.Connectors) that names a
// connector the project doesn't have, which is a typo far more often than a
// connector somebody means to add later.
func (m *Manager) CheckConnectorLimit(ctx context.Context, project string, names []string) error {
	if names == nil {
		return nil
	}
	found, err := m.Store.Connectors(ctx, project, "")
	if err != nil {
		return err
	}
	var have, unknown []string
	for _, c := range found {
		have = append(have, c.Name)
	}
	for _, name := range names {
		if !slices.Contains(have, name) {
			unknown = append(unknown, fmt.Sprintf("%q", name))
		}
	}
	switch {
	case len(unknown) == 0:
		return nil
	case len(have) == 0:
		return fmt.Errorf("%s has no connectors, so it has no %s to give an agent: leave connectors out", project, strings.Join(unknown, ", "))
	}
	return fmt.Errorf("%s has no connector %s: its connectors are %s", project, strings.Join(unknown, ", "), strings.Join(have, ", "))
}

// GrantConnector gives an agent one of its project's connectors that its
// limit left out, and rewrites its MCP servers when it is running (which is
// slow: a few incus calls). An agent
// with no limit, or one that already has it, is left as it is.
func (m *Manager) GrantConnector(ctx context.Context, a state.Agent, name string) error {
	if a.GetsConnector(name) {
		return nil
	}
	if err := m.Store.SetAgentConnectors(ctx, a.Project, a.Name, append(slices.Clone(a.Connectors), name)); err != nil {
		return err
	}
	// Given either way: a machine that isn't running gets it when it is next
	// set up.
	if err := m.SyncConnectors(ctx, a.Project, a.Name); err != nil {
		m.logf("%v", err)
	}
	return nil
}

// SyncConnectors rewrites the MCP servers of every running agent a change to
// connectors reaches — all of a project's, and its chat's, when agent is "",
// one otherwise — so that the next session of its AI tool has them. A session already running
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
	if agent == "" {
		if err := m.syncLeadConnectors(ctx, project); err != nil {
			failed = append(failed, fmt.Sprintf("%s's chat (%v)", project, err))
		}
	}
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

// leadMCPServers is the lead's Claude Code's mcpServers: AgentBox's own tools,
// and each of its project's enabled connectors. The lead runs on the host, but
// a connector reaches it the way it reaches an agent: the same relay,
// `agentbox connector mcp <name>`, pointed at the lead's socket instead of an
// agent's, which relays its project's connectors and holds no token either.
func (m *Manager) leadMCPServers(socket string, connectors []string) map[string]any {
	servers := map[string]any{
		"agentbox": map[string]any{
			"type": "stdio", "command": m.Binary, "args": []string{"mcp"},
			"env": map[string]string{"AGENTBOX_SOCKET": socket, "AGENTBOX_NO_AUTOSTART": "1"},
		},
	}
	for _, name := range connectors {
		servers[name] = map[string]any{
			"type": "stdio", "command": m.Binary, "args": []string{"connector", "mcp", name},
			"env": map[string]string{"AGENTBOX_IN_AGENT_SOCKET": socket},
		}
	}
	return servers
}

// syncLeadConnectors rewrites a project's chat's mcpServers after a change to
// its project's connectors, leaving the rest of its ~/.claude.json — Claude
// Code's own state — as it was. A project never chatted with has nothing to
// rewrite: configureLead gives the chat its connectors when it is made.
func (m *Manager) syncLeadConnectors(ctx context.Context, project string) error {
	a, err := m.Lead(ctx, project)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	socket := m.LeadSocket(project)
	if socket == "" || m.Binary == "" {
		return nil
	}
	names, err := m.connectorNames(ctx, a)
	if err != nil {
		return err
	}
	path := filepath.Join(m.Paths.LeadHome(project), ".claude.json")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged, err := withMCPServers(existing, m.leadMCPServers(socket, names))
	if err != nil {
		return err
	}
	return os.WriteFile(path, merged, 0o600)
}
