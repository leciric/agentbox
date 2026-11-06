package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/opencode"
)

// Each tool's configuration gives the lead's MCP server the environment that
// points it at the project's socket: without it, `agentbox mcp` reaches no
// project.
func TestLeadToolConfigsCarryTheServersEnvironment(t *testing.T) {
	servers := map[string]any{
		"agentbox": map[string]any{"type": "stdio", "command": "/bin/agentbox", "args": []string{"mcp"},
			"env": map[string]string{"AGENTBOX_SOCKET": "/run/lead.sock", "AGENTBOX_NO_AUTOSTART": "1"}},
	}
	codex := leadCodexConfig("/work/lead", servers)
	for _, want := range []string{`[projects."/work/lead"]`, "[mcp_servers.agentbox]", `command = "/bin/agentbox"`, `args = ["mcp"]`, `env = { AGENTBOX_NO_AUTOSTART = "1", AGENTBOX_SOCKET = "/run/lead.sock" }`} {
		if !strings.Contains(codex, want) {
			t.Errorf("config.toml = %s\nwant %s", codex, want)
		}
	}
	var leadOpenCode struct {
		MCP map[string]struct {
			Command     []string          `json:"command"`
			Environment map[string]string `json:"environment"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(leadOpenCodeConfig(opencode.V1, servers), &leadOpenCode); err != nil {
		t.Fatal(err)
	}
	if s := leadOpenCode.MCP["agentbox"]; !slices.Equal(s.Command, []string{"/bin/agentbox", "mcp"}) || s.Environment["AGENTBOX_SOCKET"] != "/run/lead.sock" {
		t.Errorf("opencode.json's agentbox = %+v", s)
	}
	var cursorMCP struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(leadCursorMCP(servers), &cursorMCP); err != nil {
		t.Fatal(err)
	}
	if cursorMCP.MCPServers["agentbox"].Env["AGENTBOX_SOCKET"] != "/run/lead.sock" {
		t.Errorf("mcp.json's agentbox = %+v", cursorMCP.MCPServers["agentbox"])
	}
}

// OpenCode 2's config keeps each local server's environment. Its compatibility
// layer reads OpenCode 1's flat map but drops that field, and the lead's
// AgentBox server is reached through AGENTBOX_SOCKET in exactly it.
func TestLeadOpenCode2ConfigKeepsTheEnvironment(t *testing.T) {
	servers := map[string]any{
		"agentbox": map[string]any{"type": "stdio", "command": "/bin/agentbox", "args": []string{"mcp"},
			"env": map[string]string{"AGENTBOX_SOCKET": "/run/lead.sock"}},
	}
	var config struct {
		MCP struct {
			Servers map[string]struct {
				Environment map[string]string `json:"environment"`
			} `json:"servers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(leadOpenCodeConfig(opencode.V2, servers), &config); err != nil {
		t.Fatal(err)
	}
	if config.MCP.Servers["agentbox"].Environment["AGENTBOX_SOCKET"] != "/run/lead.sock" {
		t.Fatalf("OpenCode 2 lead config lost the socket: %s", leadOpenCodeConfig(opencode.V2, servers))
	}
}
