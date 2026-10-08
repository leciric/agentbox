package agent

import (
	"context"
	"encoding/json"
	"os"

	"agentbox/internal/cursor"
	"agentbox/internal/state"
)

// Where a Cursor agent's files go, under its user's home.
const (
	// cursorAuthFile is the SDK's own credentials file (FileCredentialStore's
	// default path), which it reads the sign-in from.
	cursorAuthFile = ".cursor/sdk/auth.json"
	// cursorMCPFile is the MCP servers, in the cursor-agent command line's
	// format, which the adapter reads and hands the SDK (adapter.mjs,
	// mcpServers).
	cursorMCPFile = ".cursor/mcp.json"
	// cursorScriptFile is the adapter, written as each chat starts.
	cursorScriptFile = ".local/share/agentbox/" + cursor.ScriptName
)

// cursorMCPConfig is ~/.cursor/mcp.json: every MCP server AgentBox gives
// every tool, as {"mcpServers": {name: {command, args}}}.
func cursorMCPConfig(servers []mcpServer) []byte {
	out := map[string]any{}
	for _, s := range servers {
		out[s.name] = map[string]any{"type": "stdio", "command": s.command, "args": s.args}
	}
	b, _ := json.Marshal(map[string]any{"mcpServers": out})
	return b
}

// prepareAgentCursorSettings rewrites a Cursor agent's MCP servers whole, as
// configure does at creation, and copies the sign-in in again, so one made
// after the agent was created reaches it the next time its chat starts.
func (m *Manager) prepareAgentCursorSettings(ctx context.Context, a state.Agent) error {
	home := "/home/" + m.User.Name
	connectors, err := m.connectorNames(ctx, a)
	if err != nil {
		return err
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, home+"/"+cursorMCPFile, cursorMCPConfig(agentMCPServers(home, connectors)), m.User.UID, m.User.GID, 0o600); err != nil {
		return err
	}
	if a.AI != "cursor" || !m.Creds.HasCursorLogin() {
		return nil
	}
	auth, err := os.ReadFile(m.Creds.CursorAuthPath())
	if err != nil {
		return err
	}
	return m.Incus.WriteFile(ctx, a.Instance, home+"/"+cursorAuthFile, auth, m.User.UID, m.User.GID, 0o600)
}
