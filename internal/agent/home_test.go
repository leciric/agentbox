package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/state"
)

// The Home chat's tools are the agentbox MCP server, told it is the Home
// chat's so it serves the tools that reach every project.
func TestEnsureHomeRegistersItsTools(t *testing.T) {
	f := leadFixture(t)
	f.m.Binary = "/usr/bin/agentbox"
	f.m.LeadSocketPath = func(project string) string { return filepath.Join("/run", project+".sock") }
	a, err := f.m.EnsureHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != state.AgentReady || a.Ref() != state.HomeProject+"/"+state.LeadName {
		t.Errorf("EnsureHome() = %+v", a)
	}
	raw, err := os.ReadFile(filepath.Join(f.m.Paths.LeadHome(state.HomeProject), ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
		Projects map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	env := cfg.MCPServers["agentbox"].Env
	if env["AGENTBOX_CHAT"] != "home" || env["AGENTBOX_SOCKET"] != "/run/_home.sock" {
		t.Errorf("the MCP server's environment = %v, want the Home chat's socket and AGENTBOX_CHAT=home", env)
	}
	if _, ok := cfg.Projects[a.Worktree]; !ok {
		t.Errorf("its folder %s isn't trusted: %v", a.Worktree, cfg.Projects)
	}
	if f.m.Home().Status != state.AgentReady {
		t.Error("Home() doesn't know the chat has been readied")
	}
}
