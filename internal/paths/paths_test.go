package paths

import (
	"path/filepath"
	"testing"
)

func TestWorktrees(t *testing.T) {
	p := Paths{Data: "/home/alice/.local/share/agentbox"}
	t.Setenv("AGENTBOX_WORKTREES", "")
	if got, want := p.Worktree("app", "agent-01"), "/home/alice/.local/share/agentbox/worktrees/app/agent-01"; got != want {
		t.Errorf("by default: got %s, want %s", got, want)
	}
	// On a Mac the daemon's state is in the VM and the worktrees on the Mac's share.
	t.Setenv("AGENTBOX_WORKTREES", "/Users/alice/.local/share/agentbox/worktrees")
	if got, want := p.Worktree("app", "agent-01"), filepath.Join("/Users/alice/.local/share/agentbox/worktrees", "app", "agent-01"); got != want {
		t.Errorf("with AGENTBOX_WORKTREES: got %s, want %s", got, want)
	}
	t.Setenv("AGENTBOX_WORKTREES", "relative/path")
	if got := p.Worktrees(); got != "/home/alice/.local/share/agentbox/worktrees" {
		t.Errorf("a relative AGENTBOX_WORKTREES was used: %s", got)
	}
}
