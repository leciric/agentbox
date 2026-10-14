package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// stubCodex puts a do-nothing codex-acp where the host's copy would be, with
// the pinned version's manifest, and node and gh beside it, so nothing is
// downloaded.
func stubCodex(t *testing.T, m *agent.Manager) string {
	t.Helper()
	stub := []byte("#!/bin/sh\nexit 0\n")
	write := func(path string, content []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	tools := m.Paths.Tools()
	for _, bin := range []string{"node", "npm"} {
		write(filepath.Join(tools, "node", "bin", bin), stub, 0o755)
	}
	write(m.GHPath(), stub, 0o755)
	pkg := image.Pin("npm:@agentclientprotocol/codex-acp")
	version := pkg[strings.LastIndex(pkg, "@")+1:]
	write(filepath.Join(tools, "node_modules", "@agentclientprotocol", "codex-acp", "package.json"), []byte(`{"version":"`+version+`"}`), 0o644)
	adapter := filepath.Join(tools, "node_modules", ".bin", "codex-acp")
	write(adapter, stub, 0o755)
	return adapter
}

func chooseLeadTool(t *testing.T, st *state.Store, project, tool, account string) {
	t.Helper()
	if err := st.SaveChat(context.Background(), project, state.LeadName, state.Chat{Tool: tool, Account: account}); err != nil {
		t.Fatal(err)
	}
}

// A lead switched to Codex runs Codex's adapter, on AgentBox's Codex login,
// with its brief and MCP servers where Codex reads them; and it is refused,
// as an agent would be, while AgentBox has no Codex login.
func TestLeadRunsTheToolItsChatChose(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	chooseLeadTool(t, f.st, "hello-stack", "codex", "")
	if _, err := f.m.EnsureLead(ctx, "hello-stack"); err == nil || !strings.Contains(err.Error(), "Codex login") {
		t.Fatalf("EnsureLead() with no Codex login: %v, want it refused", err)
	}
	if err := os.MkdirAll(filepath.Dir(f.m.Creds.CodexAuthPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.m.Creds.CodexAuthPath(), []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if a.AI != "codex" || a.ClaudeAccount != "" {
		t.Errorf("EnsureLead() runs %q on the Claude Code account %q, want codex on none", a.AI, a.ClaudeAccount)
	}
	home := f.m.Paths.LeadHome("hello-stack")
	for _, rel := range []string{".codex/auth.json", ".codex/AGENTS.md", ".codex/config.toml", ".config/opencode/AGENTS.md", "AGENTBOX.md"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("the lead's HOME has no %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local/share/opencode/auth.json")); err == nil {
		t.Error("the lead's HOME has an OpenCode login AgentBox doesn't have")
	}

	adapter := stubCodex(t, f.m)
	cmd, err := f.m.LeadChatCommand(ctx, a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != adapter {
		t.Errorf("the lead's chat runs %s, want %s", cmd.Path, adapter)
	}
	if !slices.Contains(cmd.Env, "CODEX_HOME="+filepath.Join(home, ".codex")) {
		t.Errorf("env %v has no CODEX_HOME in the lead's HOME", cmd.Env)
	}
	if slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN=") }) {
		t.Error("a Codex lead was given a Claude Code token")
	}

	// Back on Claude Code, the row follows the chat.
	chooseLeadTool(t, f.st, "hello-stack", "claude", "")
	if a, err = f.m.EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if a.AI != "claude" || a.ClaudeAccount != "default" {
		t.Errorf("EnsureLead() after switching back runs %q on %q, want claude on default", a.AI, a.ClaudeAccount)
	}
}

// The account a lead's composer chose wins over the project's while the
// project may use it, and gives way to it once it may not.
func TestLeadRunsOnTheAccountItsChatChose(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	if err := f.m.Creds.SaveClaudeToken("personal", "personal-token"); err != nil {
		t.Fatal(err)
	}
	chooseLeadTool(t, f.st, "hello-stack", "claude", "personal")
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if a.ClaudeAccount != "personal" {
		t.Errorf("the lead runs on %q, want the chosen personal", a.ClaudeAccount)
	}
	if err := f.st.SetProjectClaudeAccounts(ctx, "hello-stack", "default", []string{"default"}); err != nil {
		t.Fatal(err)
	}
	if a, err = f.m.EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if a.ClaudeAccount != "default" {
		t.Errorf("the lead runs on %q once the project may not use personal, want default", a.ClaudeAccount)
	}
}

// The Home chat has no row: its tool and account come from its chat alone.
func TestHomeRunsTheToolItsChatChose(t *testing.T) {
	f := leadFixture(t)
	if got := f.m.Home().AI; got != "claude" {
		t.Errorf("Home() runs %q before any choice, want claude", got)
	}
	chooseLeadTool(t, f.st, state.HomeProject, "opencode", "")
	if a := f.m.Home(); a.AI != "opencode" || a.ClaudeAccount != "" {
		t.Errorf("Home() runs %q on %q, want opencode on no Claude Code account", a.AI, a.ClaudeAccount)
	}
	if _, err := f.m.EnsureHome(context.Background()); err == nil || !strings.Contains(err.Error(), "OpenCode login") {
		t.Errorf("EnsureHome() on OpenCode with no login: %v, want it refused", err)
	}
}
