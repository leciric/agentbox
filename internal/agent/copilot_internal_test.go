package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// An agent's mcp-config.json gives Copilot every server with all its tools,
// and a day to the one that waits on a person; a lead's carries each
// server's environment.
func TestCopilotMCPConfig(t *testing.T) {
	b := copilotMCPConfig([]mcpServer{{name: "memory", command: "agentbox", args: []string{"mcp"}, waits: true}, {name: "playwright", command: "npx"}},
		map[string]map[string]string{"memory": {"AGENTBOX_SOCKET": "/run/a.sock"}})
	var c struct {
		MCPServers map[string]struct {
			Type    string
			Command string
			Args    []string
			Tools   []string
			Timeout int
			Env     map[string]string
		}
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	memory, playwright := c.MCPServers["memory"], c.MCPServers["playwright"]
	if memory.Type != "local" || memory.Command != "agentbox" || len(memory.Tools) != 1 || memory.Tools[0] != "*" || memory.Timeout != copilotMCPTimeout || memory.Env["AGENTBOX_SOCKET"] != "/run/a.sock" {
		t.Errorf("memory = %+v", memory)
	}
	if playwright.Timeout != 0 || playwright.Args == nil || playwright.Env != nil {
		t.Errorf("playwright = %+v", playwright)
	}
}

// Its settings.json keeps it on the pinned version and trusts the worktree.
func TestCopilotSettings(t *testing.T) {
	var s struct {
		AutoUpdate     *bool
		TrustedFolders []string
	}
	if err := json.Unmarshal(copilotSettings("/home/dev/work"), &s); err != nil {
		t.Fatal(err)
	}
	if s.AutoUpdate == nil || *s.AutoUpdate || len(s.TrustedFolders) != 1 || s.TrustedFolders[0] != "/home/dev/work" {
		t.Errorf("settings = %+v", s)
	}
}

// The binary is npm's platform package for the machine.
func TestCopilotPlatform(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "linux-x64"}, {"linux", "arm64", "linux-arm64"}, {"darwin", "arm64", "darwin-arm64"}, {"windows", "amd64", "win32-x64"},
	} {
		if got := copilotPlatform(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("copilotPlatform(%s, %s) = %s, want %s", tc.goos, tc.goarch, got, tc.want)
		}
	}
	if !strings.HasPrefix(leadNPMTools["copilot"].bin, "@github/copilot-") || !strings.HasPrefix(leadNPMTools["copilot"].pkg, "@github/copilot@") {
		t.Errorf("a lead's Copilot = %+v", leadNPMTools["copilot"])
	}
}

// CopilotModels' Copilot runs in its throwaway home on the account's token,
// with nothing of the daemon's own GitHub token.
func TestCopilotModelsEnv(t *testing.T) {
	env := strings.Join(copilotModelsEnv("/t/h", "/node/bin", "github_pat_x"), "\n")
	for _, want := range []string{"HOME=/t/h", "COPILOT_HOME=/t/h/.copilot", "COPILOT_GITHUB_TOKEN=github_pat_x", copilotNoUpdate} {
		if !strings.Contains(env, want) {
			t.Errorf("env %q lacks %q", env, want)
		}
	}
	if strings.Contains(env, "GH_TOKEN") {
		t.Errorf("env %q has a GH_TOKEN", env)
	}
}
