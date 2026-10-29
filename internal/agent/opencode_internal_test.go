package agent

import (
	"encoding/json"
	"slices"
	"testing"

	"agentbox/internal/opencode"
)

// The MCP servers an agent's AI tool drives its display with are the same two
// for every tool; only the shape of the file differs. OpenCode's is JSON, with
// the command and its arguments as one argv array.
func TestOpenCodeConfig(t *testing.T) {
	t.Parallel()
	servers := []mcpServer{
		{"playwright", "/home/dev/.local/share/mise/shims/playwright-mcp", []string{"--cdp-endpoint", BrowserDevTools}, false},
		{"desktop", AgentBinaryPath, []string{"desktop", "mcp"}, false},
	}
	parse := func(t *testing.T, autonomous bool) map[string]any {
		t.Helper()
		raw, err := openCodeConfig(opencode.V1, servers, autonomous)
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
	t.Parallel()
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

// OpenCode 2 reads its servers under mcp.servers and its permissions as an
// ordered ruleset. Its compatibility layer would still read OpenCode 1's flat
// map, but drops each server's environment there, which is why both shapes are
// written rather than one left to the other.
func TestOpenCodeConfigVersionTwo(t *testing.T) {
	t.Parallel()
	servers := []mcpServer{{"agentbox", "/bin/agentbox", []string{"mcp"}, false}}
	parse := func(t *testing.T, autonomous bool) map[string]any {
		t.Helper()
		raw, err := openCodeConfig(opencode.V2, servers, autonomous)
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
	mcp, ok := config["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp isn't an object: %v", config["mcp"])
	}
	byName, ok := mcp["servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp has no servers: %v", mcp)
	}
	if _, ok := byName["agentbox"]; !ok {
		t.Fatalf("the agentbox server is missing: %v", byName)
	}
	permissions, ok := config["permissions"].([]any)
	if !ok || len(permissions) != 1 {
		t.Fatalf("autonomous agents get no wildcard permission: %v", config["permissions"])
	}
	rule, _ := permissions[0].(map[string]any)
	if rule["action"] != "*" || rule["effect"] != "allow" {
		t.Fatalf("permissions = %v", permissions)
	}
	if _, ok := config["subagent_depth"]; ok {
		t.Error("OpenCode 2's config still carries subagent_depth, which it ignores")
	}
	if _, ok := config["permission"]; ok {
		t.Error("OpenCode 2's config still carries OpenCode 1's permission shorthand")
	}
	if _, ok := parse(t, false)["permissions"]; ok {
		t.Error("an agent that asks got a permission ruleset")
	}
	experimental, ok := config["experimental"].(map[string]any)
	if !ok || experimental["todo"] != true {
		t.Errorf("experimental = %v, want todos offered so AgentBox can show them as the plan", config["experimental"])
	}
	if _, ok := parse(t, false)["experimental"]; !ok {
		t.Error("an agent that asks got no experimental settings")
	}
}

// OpenCode 1 exposes todowrite without a setting, and does not read
// experimental.todo.
func TestOpenCodeOneDoesNotSetExperimentalTodo(t *testing.T) {
	t.Parallel()
	raw, err := openCodeConfig(opencode.V1, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["experimental"]; ok {
		t.Errorf("OpenCode 1 config = %s, want no experimental settings", raw)
	}
}
