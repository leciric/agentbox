package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// An agent's settings.json names its sign-in, trusts AgentBox's MCP servers
// (one that waits on a person gets a day), reads AGENTS.md, and turns off what
// would stop or change the pinned tool and what would report on its use.
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
		Context   struct{ FileName []string }
		General   struct{ EnableAutoUpdate *bool }
		Privacy   struct{ UsageStatisticsEnabled *bool }
		Telemetry struct {
			Enabled    *bool
			LogPrompts *bool
		}
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
	off := func(b *bool) bool { return b != nil && !*b }
	if !off(s.Privacy.UsageStatisticsEnabled) || !off(s.Telemetry.Enabled) || !off(s.Telemetry.LogPrompts) {
		t.Errorf("usage statistics or telemetry not off: %s", b)
	}

	// Every other settings.json AgentBox writes has the same: a lead's, the
	// Home chat's on the user's computer, and GeminiModels' throwaway one
	// (geminiSettings with no servers).
	for name, b := range map[string][]byte{"lead": leadGeminiSettings(nil), "host": []byte(hostGeminiSettings()), "models": geminiSettings(nil, "")} {
		s.Privacy.UsageStatisticsEnabled, s.Telemetry.Enabled = nil, nil
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		if !off(s.Privacy.UsageStatisticsEnabled) || !off(s.Telemetry.Enabled) {
			t.Errorf("the %s settings.json leaves usage statistics or telemetry on: %s", name, b)
		}
	}
}

// GeminiModels' Gemini CLI stays in the process it starts, as a chat's does,
// so the deadline that kills it leaves nothing behind.
func TestGeminiModelsEnv(t *testing.T) {
	env := geminiModelsEnv("/t/home", "/node/bin")
	if !slices.Contains(env, geminiNoRelaunch) || !slices.Contains(env, "HOME=/t/home") {
		t.Errorf("env = %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "GEMINI_API_KEY=") {
			t.Errorf("the key is in its environment: %v", env)
		}
	}
}

// An API key login goes over ACP, in authenticate's _meta, as Gemini CLI's
// "gemini-api-key" method takes it.
func TestGeminiKeyAuth(t *testing.T) {
	b, _ := json.Marshal(geminiKeyAuth("AIza-x"))
	if string(b) != `{"methodId":"gemini-api-key","_meta":{"api-key":"AIza-x"}}` {
		t.Errorf("authenticate = %s", b)
	}
}

// A lead's Google sign-in goes in as the files Gemini CLI reads and the
// sign-in in its settings.json. An API key login is written nowhere (the chat
// sends it), and the key file an earlier AgentBox wrote goes.
func TestWriteLeadGeminiLogin(t *testing.T) {
	home := t.TempDir()
	if err := writePrivate(home, geminiSettingsFile, leadGeminiSettings(map[string]any{
		"agentbox": map[string]any{"command": "agentbox", "args": []string{"mcp"}, "env": map[string]string{"AGENTBOX_SOCKET": "/s"}},
	})); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(home, geminiKeyFile, []byte("AIza-old")); err != nil {
		t.Fatal(err)
	}
	key, _ := credentials.GeminiAPIKeyAuth("AIza-lead")
	if err := writeLeadGeminiLogin(home, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, geminiKeyFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the key file is still there: %v", err)
	}
	settings, _ := os.ReadFile(filepath.Join(home, geminiSettingsFile))
	var s struct {
		MCPServers map[string]struct{ Env map[string]string }
		Security   struct{ Auth struct{ SelectedType string } }
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatal(err)
	}
	if s.Security.Auth.SelectedType != "" || s.MCPServers["agentbox"].Env["AGENTBOX_SOCKET"] != "/s" || strings.Contains(string(settings), "AIza") {
		t.Errorf("settings.json = %s", settings)
	}

	if err := writeLeadGeminiLogin(home, []byte(`{"access_token":"a","refresh_token":"r"}`)); err != nil {
		t.Fatal(err)
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

// settings.json names an API key login only where Gemini CLI is given the key
// with it: the agent's own Gemini CLI on the command line. Anywhere else it
// would stop on a login it can't find, and a chat signs itself in.
func TestGeminiAuthTypeOnlyWithALogin(t *testing.T) {
	ctx := context.Background()
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}}
	home := state.Agent{Project: state.HomeProject, Role: state.RoleLead, AI: "claude"}
	if got := m.geminiAuthType(ctx, home); got != "" {
		t.Errorf("no login: %q", got)
	}
	key, _ := credentials.GeminiAPIKeyAuth("AIza-x")
	if err := m.Creds.SaveToolAccount("gemini", "", "", key); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		a    state.Agent
		want string
	}{
		{home, ""},
		{state.Agent{AI: "gemini", Interface: state.InterfaceChat}, ""},
		{state.Agent{AI: "gemini", Interface: state.InterfaceCLI}, "gemini-api-key"},
	} {
		if got := m.geminiAuthType(ctx, c.a); got != c.want {
			t.Errorf("%s on %q: %q, want %q", c.a.AI, c.a.Interface, got, c.want)
		}
	}
	if err := m.Creds.SaveToolAccount("gemini", "", "", []byte(`{"access_token":"a","refresh_token":"r"}`)); err != nil {
		t.Fatal(err)
	}
	if got := m.geminiAuthType(ctx, home); got != "oauth-personal" {
		t.Errorf("a Google sign-in: %q", got)
	}
}
