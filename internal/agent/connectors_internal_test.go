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

// sameMCPServers looks only at the MCP servers in each tool's file: what else
// the tool keeps there may change without its chat needing a restart.
func TestSameMCPServers(t *testing.T) {
	t.Parallel()
	servers := agentMCPServers("/home/dev", []string{"notion"})
	more := agentMCPServers("/home/dev", []string{"notion", "linear"})
	claude := func(state string, s []mcpServer) []byte {
		b, err := withMCPServers([]byte(state), claudeMCPServers(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	opencode := func(s []mcpServer, autonomous bool) []byte {
		b, err := openCodeConfig(s, autonomous)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, c := range []struct {
		name, ai string
		was, now []byte
		same     bool
	}{
		{"claude, its own state changed", "claude", claude(`{"numStartups":1}`, servers), claude(`{"numStartups":7,"projects":{}}`, servers), true},
		{"claude, a connector added", "claude", claude(`{}`, servers), claude(`{}`, more), false},
		{"claude, no file before", "claude", nil, claude(`{}`, servers), false},
		{"codex, the compact window changed", "codex", []byte(codexConfigFor("/w", servers, 100)), []byte(codexConfigFor("/w", servers, 200) + "\n[notice]\nhide = true\n"), true},
		{"codex, a connector added", "codex", []byte(codexConfigFor("/w", servers, 100)), []byte(codexConfigFor("/w", more, 100)), false},
		{"opencode, permissions changed", "opencode", opencode(servers, false), opencode(servers, true), true},
		{"opencode, a connector added", "opencode", opencode(servers, false), opencode(more, false), false},
		{"cursor, unchanged", "cursor", cursorMCPConfig(servers), cursorMCPConfig(servers), true},
		{"cursor, a connector added", "cursor", cursorMCPConfig(servers), cursorMCPConfig(more), false},
	} {
		if got := sameMCPServers(c.ai, c.was, c.now); got != c.same {
			t.Errorf("%s: sameMCPServers = %v, want %v", c.name, got, c.same)
		}
	}
}
