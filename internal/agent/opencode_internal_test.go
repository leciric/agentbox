package agent

import (
	"encoding/json"
	"slices"
	"testing"
)

// The MCP servers an agent's AI tool drives its display with are the same two
// for every tool; only the shape of the file differs. OpenCode's is JSON, with
// the command and its arguments as one argv array.
func TestOpenCodeConfig(t *testing.T) {
	servers := []mcpServer{
		{"playwright", "/home/dev/.local/share/mise/shims/playwright-mcp", []string{"--cdp-endpoint", BrowserDevTools}, false},
		{"desktop", AgentBinaryPath, []string{"desktop", "mcp"}, false},
	}
	parse := func(t *testing.T, autonomous bool) map[string]any {
		t.Helper()
		raw, err := openCodeConfig(servers, autonomous)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatalf("opencode.json isn't JSON: %v: %s", err, raw)
		}
		return config
	}

	config := parse(t, true)
	if config["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v", config["$schema"])
	}
	// OpenCode's own equivalent of Claude Code's subagent spawn-depth cap
	// (D84); it has no concurrency cap to set beside it, and no absolute
	// compact window at all.
	if config["subagent_depth"] != float64(opencodeSubagentDepth) {
		t.Errorf("subagent_depth = %v, want %d", config["subagent_depth"], opencodeSubagentDepth)
	}
	mcp, ok := config["mcp"].(map[string]any)
	if !ok || len(mcp) != len(servers) {
		t.Fatalf("mcp = %v, want one entry per server", config["mcp"])
	}
	desktop, ok := mcp["desktop"].(map[string]any)
	if !ok {
		t.Fatalf("no desktop server: %v", mcp)
	}
	if desktop["type"] != "local" || desktop["enabled"] != true {
		t.Errorf("the desktop server = %v", desktop)
	}
	var argv []string
	for _, part := range desktop["command"].([]any) {
		argv = append(argv, part.(string))
	}
	if want := []string{AgentBinaryPath, "desktop", "mcp"}; !slices.Equal(argv, want) {
		t.Errorf("the desktop server's command = %v, want %v", argv, want)
	}

	// An autonomous agent's machine is its sandbox, and there is nobody at its
	// chat to answer; one that stops to ask gets no permission key at all.
	if config["permission"] != "allow" {
		t.Errorf("an autonomous agent's permission = %v, want every permission allowed", config["permission"])
	}
	if got, has := parse(t, false)["permission"]; has {
		t.Errorf("an agent that asks first got permission = %v", got)
	}
}

// The adapter that runs OpenCode for the chat is OpenCode itself, behind a
// subcommand — which is what ChatAdapter.Args exists for.
func TestOpenCodeIsItsOwnAdapter(t *testing.T) {
	adapter, ok := ChatAdapters["opencode"]
	if !ok {
		t.Fatal("no chat adapter for OpenCode")
	}
	if adapter.Command != "opencode" || !slices.Equal(adapter.Args, []string{"acp"}) {
		t.Errorf("the OpenCode adapter runs %q %v", adapter.Command, adapter.Args)
	}
	if _, ok := Tools["opencode"]; !ok {
		t.Error("no command line for OpenCode agents")
	}
}
