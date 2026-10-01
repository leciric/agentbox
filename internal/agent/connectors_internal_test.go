package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/connectors"
)

// A connector named like one of the servers AgentBox gives every agent would
// replace it: every one of them is reserved.
func TestConnectorsCantTakeABuiltInServersName(t *testing.T) {
	t.Parallel()
	for _, s := range agentMCPServers("/home/dev", nil) {
		if !slices.Contains(connectors.Reserved, s.name) {
			t.Errorf("the MCP server %q isn't in connectors.Reserved", s.name)
		}
	}
}

func TestAgentMCPServersIncludeConnectors(t *testing.T) {
	t.Parallel()
	servers := agentMCPServers("/home/dev", []string{"notion", "linear"})
	claude := claudeMCPServers(servers)
	for _, name := range []string{"desktop", "playwright"} {
		if _, ok := claude[name]; ok {
			t.Errorf("Claude Code's own servers include %s, which is its desktop subagent's", name)
		}
	}
	notion, _ := json.Marshal(claude["notion"])
	if string(notion) != `{"args":["connector","mcp","notion"],"command":"/usr/local/bin/agentbox","type":"stdio"}` {
		t.Errorf("Claude Code's notion = %s", notion)
	}
	if codex := codexConfigFor("/w", servers, 0); !strings.Contains(codex, "[mcp_servers.linear]\ncommand = \"/usr/local/bin/agentbox\"\nargs = [\"connector\", \"mcp\", \"linear\"]\n") {
		t.Errorf("Codex's config:\n%s", codex)
	}
	opencode, err := openCodeConfig(servers, false)
	if err != nil || !strings.Contains(string(opencode), `"notion"`) {
		t.Errorf("OpenCode's config = %s, %v", opencode, err)
	}
}

// Claude Code keeps its own state in ~/.claude.json: rewriting its MCP
// servers leaves the rest alone, and a file that isn't JSON isn't overwritten.
func TestWithMCPServers(t *testing.T) {
	t.Parallel()
	merged, err := withMCPServers([]byte(`{"hasCompletedOnboarding":true,"numStartups":7,"mcpServers":{"old":{}}}`), map[string]any{"notion": map[string]any{"type": "stdio"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(merged) != `{"hasCompletedOnboarding":true,"mcpServers":{"notion":{"type":"stdio"}},"numStartups":7}` {
		t.Errorf("merged = %s", merged)
	}
	if merged, err := withMCPServers(nil, map[string]any{}); err != nil || string(merged) != `{"mcpServers":{}}` {
		t.Errorf("from nothing = %s, %v", merged, err)
	}
	if _, err := withMCPServers([]byte("not json"), nil); err == nil {
		t.Error("a ~/.claude.json that isn't JSON was overwritten")
	}
}
