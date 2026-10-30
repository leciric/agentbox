package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// An agent's settings.json names its sign-in, trusts AgentBox's MCP servers
// (one that waits on a person gets a day), reads AGENTS.md, and turns off what
// would stop or change the pinned tool.
func TestGeminiSettings(t *testing.T) {
	b := geminiSettings([]mcpServer{{name: "memory", command: "agentbox", args: []string{"mcp"}, waits: true}, {name: "playwright", command: "npx"}}, "oauth-personal")
	var s struct {
		MCPServers map[string]struct {
			Command string
			Trust   bool
			Timeout int
		}
		Security struct {
			Auth        struct{ SelectedType string }
			FolderTrust struct{ Enabled bool }
		}
		Context struct{ FileName []string }
		General struct{ EnableAutoUpdate *bool }
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	memory := s.MCPServers["memory"]
	if memory.Command != "agentbox" || !memory.Trust || memory.Timeout != geminiMCPTimeout || s.MCPServers["playwright"].Timeout != 0 {
		t.Errorf("mcpServers = %+v", s.MCPServers)
	}
	if s.Security.Auth.SelectedType != "oauth-personal" || s.Security.FolderTrust.Enabled || s.General.EnableAutoUpdate == nil || *s.General.EnableAutoUpdate {
		t.Errorf("settings = %s", b)
	}
	if len(s.Context.FileName) != 2 || s.Context.FileName[1] != "AGENTS.md" {
		t.Errorf("context.fileName = %v", s.Context.FileName)
	}
}

// A lead's login goes in as the files Gemini CLI reads and the sign-in in its
// settings.json, and an API key login's file goes when it moves to Google.
func TestWriteLeadGeminiLogin(t *testing.T) {
	home := t.TempDir()
	if err := writePrivate(home, geminiSettingsFile, leadGeminiSettings(map[string]any{
		"agentbox": map[string]any{"command": "agentbox", "args": []string{"mcp"}, "env": map[string]string{"AGENTBOX_SOCKET": "/s"}},
	})); err != nil {
		t.Fatal(err)
	}
	key, _ := credentials.GeminiAPIKeyAuth("AIza-lead")
	if err := writeLeadGeminiLogin(home, key); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	if env := m.leadGeminiEnv(state.Agent{Project: "pawly", Name: state.LeadName, Role: state.RoleLead}, home); len(env) != 1 || env[0] != "GEMINI_API_KEY=AIza-lead" {
		t.Errorf("leadGeminiEnv = %v", env)
	}
	settings, _ := os.ReadFile(filepath.Join(home, geminiSettingsFile))
	var s struct {
		MCPServers map[string]struct{ Env map[string]string }
		Security   struct{ Auth struct{ SelectedType string } }
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatal(err)
	}
	if s.Security.Auth.SelectedType != "gemini-api-key" || s.MCPServers["agentbox"].Env["AGENTBOX_SOCKET"] != "/s" {
		t.Errorf("settings.json = %s", settings)
	}

	if err := writeLeadGeminiLogin(home, []byte(`{"access_token":"a","refresh_token":"r"}`)); err != nil {
		t.Fatal(err)
	}
	if env := m.leadGeminiEnv(state.Agent{Project: "pawly", Name: state.LeadName, Role: state.RoleLead}, home); env != nil {
		t.Errorf("a Google sign-in still hands over a key: %v", env)
	}
	if _, err := os.Stat(filepath.Join(home, geminiOAuthFile)); err != nil {
		t.Errorf("no oauth_creds.json: %v", err)
	}
	settings, _ = os.ReadFile(filepath.Join(home, geminiSettingsFile))
	_ = json.Unmarshal(settings, &s)
	if s.Security.Auth.SelectedType != "oauth-personal" {
		t.Errorf("settings.json after a Google sign-in = %s", settings)
	}
}
