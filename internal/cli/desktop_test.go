package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopMCPNeedsAnAgent(t *testing.T) {
	// Not in an agent — even when the tests run inside one, whose own
	// hostname (insideAgent's fallback) starts with "ab-".
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	t.Setenv("AGENTBOX_IN_AGENT_MARKER", filepath.Join(t.TempDir(), "no-marker"))
	t.Setenv("AGENTBOX_IN_AGENT_HOSTNAME", "front-end-host")
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"desktop", "mcp"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "runs inside an agent") {
		t.Errorf("agentbox desktop mcp outside an agent: got %v, want it to say it runs inside an agent", err)
	}
}

// TestDesktopMCPMissingSocketInsideAgent covers the window where an agent's
// systemd has mounted a fresh tmpfs over /run and hidden the socket
// (internal/agent/agentapi.go's replugHiddenSocket race): the marker alone
// says this is an agent, so the error should name the missing socket rather
// than saying this doesn't run inside an agent at all.
func TestDesktopMCPMissingSocketInsideAgent(t *testing.T) {
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	t.Setenv("AGENTBOX_IN_AGENT_HOSTNAME", "front-end-host")
	marker := filepath.Join(t.TempDir(), "agentbox-image")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_IN_AGENT_MARKER", marker)
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"desktop", "mcp"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "the in-agent API socket") || !strings.Contains(err.Error(), "is missing") {
		t.Errorf("agentbox desktop mcp inside an agent with a hidden socket: got %v, want it to name the missing socket", err)
	}
}

// TestDesktopMCPMissingSocketInsideAnOlderAgent covers an agent whose image
// predates the marker (api.InAgentMarker): with no marker and the socket
// hidden, insideAgent must fall back to the container's own hostname
// (ab-<project>-<agent>, Incus's default), not say this doesn't run inside
// an agent at all.
func TestDesktopMCPMissingSocketInsideAnOlderAgent(t *testing.T) {
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	t.Setenv("AGENTBOX_IN_AGENT_MARKER", filepath.Join(t.TempDir(), "no-marker"))
	t.Setenv("AGENTBOX_IN_AGENT_HOSTNAME", "ab-hello-stack-agent-01")
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"desktop", "mcp"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "the in-agent API socket") || !strings.Contains(err.Error(), "is missing") {
		t.Errorf("agentbox desktop mcp inside an older agent with a hidden socket: got %v, want it to name the missing socket", err)
	}
}

// Inside an agent, a host command says it doesn't work there instead of
// starting a daemon of the agent's own, an empty one that answered every
// project and agent with "not found" (agentbox chat <project>, exec, list).
func TestHostCommandInsideAgentStartsNoDaemon(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "agentbox-image")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_IN_AGENT_MARKER", marker)
	t.Setenv("PATH", t.TempDir())   // no incus: not a nesting agent
	t.Setenv("AGENTBOX_ENV", "")    // this machine's daemon, not a hub's
	t.Setenv("AGENTBOX_SOCKET", "") // nor one a running daemon's socket names
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	for _, args := range [][]string{{"list"}, {"chat", "pawly", "hello"}} {
		cmd := NewRootCmd()
		cmd.SetArgs(args)
		cmd.SetOut(new(strings.Builder))
		stderr := new(strings.Builder)
		cmd.SetErr(stderr)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "inside an agent") || !strings.Contains(err.Error(), "agentbox ask") {
			t.Errorf("agentbox %s inside an agent: got %v, want it to say it doesn't work here", strings.Join(args, " "), err)
		}
		if strings.Contains(stderr.String(), "Started the AgentBox daemon") {
			t.Errorf("agentbox %s started a daemon inside the agent", strings.Join(args, " "))
		}
	}
}
