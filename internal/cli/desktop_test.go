package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopMCPNeedsAnAgent(t *testing.T) {
	// Not in an agent — even when the tests run inside one.
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
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
