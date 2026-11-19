//go:build integration

package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// TestClaudeIdleCompaction drives the real claude-agent-acp into the
// compaction it starts by itself shortly before an idle session's one-hour
// prompt cache runs out, then sends a message, which has to get an answer.
// The delay is cut to seconds with CLAUDE_AGENT_ACP_IDLE_COMPACT_DELAY_MS;
// the context has to pass the adapter's 100k-token floor, so the first
// message carries that much text. Live only, like the test beside it;
// AGENTBOX_CLAUDE_TOKEN, when set, is the OAuth token it logs in with, and
// AGENTBOX_ACP_TRACE a directory to keep what went each way in.
func TestClaudeIdleCompaction(t *testing.T) {
	if os.Getenv("AGENTBOX_CLAUDE_LIVE") == "" {
		t.Skip("set AGENTBOX_CLAUDE_LIVE to run Claude Code for real")
	}
	adapter := os.Getenv("AGENTBOX_CLAUDE_ACP")
	if adapter == "" {
		adapter = "claude-agent-acp"
	}
	if _, err := exec.LookPath(adapter); err != nil {
		t.Skip("claude-agent-acp isn't installed on this machine")
	}
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"haiku","promptCacheTtl":"1h"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "claude", Worktree: t.TempDir()}
	m := &Manager{
		Store:   store,
		Version: "test",
		Publish: func(api.ChatEvent) {},
		Logf:    t.Logf,
		Launch: func(ctx context.Context, _ state.Agent, _ func(string)) (*Process, error) {
			cmd := exec.CommandContext(ctx, adapter)
			if dir := os.Getenv("AGENTBOX_ACP_TRACE"); dir != "" {
				// What went each way, to read when it fails.
				cmd = exec.CommandContext(ctx, "sh", "-c", `tee "$0/in.jsonl" | "$1" | tee "$0/out.jsonl"`, dir, adapter)
			}
			cmd.Dir = a.Worktree
			cmd.Env = append(os.Environ(),
				"CLAUDE_CONFIG_DIR="+config,
				"CLAUDE_AGENT_ACP_IDLE_COMPACT_DELAY_MS=20000",
				"CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS=100000",
			)
			if tok := os.Getenv("AGENTBOX_CLAUDE_TOKEN"); tok != "" {
				cmd.Env = append(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
			}
			return StartCommand(cmd)
		},
	}
	t.Cleanup(m.Wait)
	t.Cleanup(m.Close)

	wait := func(what string, within time.Duration, cond func(api.ChatThread) bool) api.ChatThread {
		t.Helper()
		deadline := time.Now().Add(within)
		for {
			th, err := m.Thread(a)
			if err != nil {
				t.Fatal(err)
			}
			if cond(th) {
				return th
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s within %s: %s", what, within, summary(th))
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	if th := wait("ready session", 90*time.Second, func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady || th.Session.Error != ""
	}); th.Session.Error != "" {
		t.Fatalf("the session failed: %s", th.Session.Error)
	}

	var big strings.Builder
	for i := 0; big.Len() < 480_000; i++ {
		fmt.Fprintf(&big, "Line %d of the ledger: account %d moved %d units to account %d on day %d.\n", i, i*7%1000, i*13%997, i*11%1000, i%365)
	}
	if _, err := m.Send(a, "Here is a ledger. Don't use any tools. Reply with one word: RECEIVED.\n\n"+big.String()); err != nil {
		t.Fatal(err)
	}
	wait("end to the first turn", 3*time.Minute, turnsEnded(1))
	t.Log("first turn ended; waiting for the idle compaction")

	compacted := func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "tool" && it.Tool.Title == "Compact conversation" && it.Tool.Status == "completed" {
				return true
			}
		}
		return false
	}
	th := wait("idle compaction", 4*time.Minute, compacted)
	t.Logf("compacted: %s", summary(th))
	time.Sleep(5 * time.Second)
	// The compaction was the adapter's own: no turn runs for it.
	if th, _ = m.Thread(a); th.Session.State != api.ChatReady {
		t.Fatalf("after the adapter's own compaction the chat is %s, want ready: %s", th.Session.State, summary(th))
	}

	if _, err := m.Send(a, "Reply with one word: PONG."); err != nil {
		t.Fatal(err)
	}
	wait("answer after the compaction", 3*time.Minute, func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "assistant" && strings.Contains(it.Text, "PONG") {
				return th.Session.State == api.ChatReady
			}
		}
		return false
	})
}
