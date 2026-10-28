package chat

import (
	"testing"

	"agentbox/internal/state"
)

// An adapter refuses a session whose cwd is empty, and a pod agent's row names
// no worktree: the tree is inside the pod, so the daemon says where it is.
func TestASessionRunsWhereTheMachineSays(t *testing.T) {
	t.Parallel()
	m := &Manager{Worktree: func(state.Agent) string { return "/workspace" }}
	if got := m.dir(state.Agent{}); got != "/workspace" {
		t.Errorf("dir = %q, want the machine's worktree", got)
	}
	m = &Manager{}
	if got := m.dir(state.Agent{Worktree: "/work/agent-01"}); got != "/work/agent-01" {
		t.Errorf("dir = %q, want the agent's own worktree", got)
	}
}
