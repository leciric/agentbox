package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

func openInternalStore(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// A chat starts the adapter at the version "Keep Claude Code up to date"
// moved agents to, installing it and Claude Code's own command line first in
// an agent made before, and at the pin when nothing moved it.
func TestTheChatRunsTheVersionAgentsWereMovedTo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	calls := filepath.Join(t.TempDir(), "calls")
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}, Store: openInternalStore(t), Incus: fakeIncus(t, `echo "$*" >>`+calls+`
case "$*" in
  list*) echo '[{"name":"ab-sai-agent-01","status":"Running"}]' ;;
  *"mise where 'npm:@agentclientprotocol/claude-agent-acp@99.0.0'"*|*"mise where 'claude@99.1.0'"*) exit 1 ;;
esac`)}
	m.User.Name = "dev"
	a := claudeAgent("")
	a.Interface = state.InterfaceChat
	start := func() (script, ran string) {
		t.Helper()
		_ = os.Remove(calls)
		cmd, err := m.ChatCommand(ctx, a, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(calls)
		return cmd.Args[len(cmd.Args)-1], string(out)
	}

	pinned := image.Pin("npm:@agentclientprotocol/claude-agent-acp")
	if script, ran := start(); !strings.Contains(script, "mise exec '"+pinned+"'") || strings.Contains(ran, "mise use") || strings.Contains(ran, "mise install") {
		t.Errorf("with nothing moved, ran %q and started %q", ran, script)
	}

	for name, v := range map[string]string{"claude": "99.1.0", "npm:@agentclientprotocol/claude-agent-acp": "99.0.0"} {
		if err := m.Store.SetToolVersion(ctx, state.ToolVersion{Name: name, Version: v}); err != nil {
			t.Fatal(err)
		}
	}
	script, ran := start()
	if !strings.Contains(script, "mise exec 'npm:@agentclientprotocol/claude-agent-acp@99.0.0'") {
		t.Errorf("the chat runs %q, not the adapter it was moved to", script)
	}
	for _, want := range []string{"MISE_YES=1 mise install 'npm:@agentclientprotocol/claude-agent-acp@99.0.0'", "MISE_YES=1 mise use -g 'claude@99.1.0'"} {
		if !strings.Contains(ran, want) {
			t.Errorf("didn't run %q:\n%s", want, ran)
		}
	}
}
