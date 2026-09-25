package agent

import (
	"strings"
	"testing"
)

// TestCodexConfigFor checks Codex's config.toml the same way TestOpenCodeConfig
// checks OpenCode's: as a pure function, on the string it produces. TOML has
// no library here to parse it back with, so the ordering assertions matter as
// much as the values — a bare key placed after a [table] header is silently
// read as belonging to that table instead of the document root.
func TestCodexConfigFor(t *testing.T) {
	servers := []mcpServer{
		{"playwright", "/home/dev/.local/share/mise/shims/playwright-mcp", []string{"--cdp-endpoint", BrowserDevTools}},
		{"desktop", AgentBinaryPath, []string{"desktop", "mcp"}},
	}

	config := codexConfigFor("/worktree", servers, 150_000)
	if !strings.Contains(config, `[projects."/worktree"]`) {
		t.Errorf("config.toml = %s, want the worktree trusted", config)
	}
	if !strings.Contains(config, "trust_level = \"trusted\"") {
		t.Errorf("config.toml = %s, want it trusted", config)
	}
	for _, want := range []string{`[mcp_servers.playwright]`, `[mcp_servers.desktop]`} {
		if !strings.Contains(config, want) {
			t.Errorf("config.toml = %s, want %s", config, want)
		}
	}
	if !strings.Contains(config, "model_auto_compact_token_limit = 150000") {
		t.Errorf("config.toml = %s, want the compact window", config)
	}
	if !strings.Contains(config, "[agents]") || !strings.Contains(config, "max_concurrent_threads_per_session = 3") {
		t.Errorf("config.toml = %s, want the subagent concurrency cap", config)
	}
	// Codex's own max_depth is documented, in the binary's own embedded help,
	// as ignored by its current agent runtime: writing it would be a cap
	// Codex itself doesn't honour.
	if strings.Contains(config, "max_depth") {
		t.Errorf("config.toml = %s, wrote max_depth, which Codex ignores", config)
	}
	if window := strings.Index(config, "model_auto_compact_token_limit"); window == -1 || window > strings.Index(config, "[") {
		t.Errorf("config.toml has a bare key after a [table] header: %s", config)
	}

	// A window of 0 is the model's whole window, said by leaving the key out
	// entirely — Codex has no "unset" value for an i64, only its absence.
	whole := codexConfigFor("/worktree", servers, 0)
	if strings.Contains(whole, "model_auto_compact_token_limit") {
		t.Errorf("config.toml(window 0) = %s, want no compact window key", whole)
	}
	if !strings.Contains(whole, "max_concurrent_threads_per_session = 3") {
		t.Errorf("config.toml(window 0) = %s, want the subagent cap regardless", whole)
	}
}
