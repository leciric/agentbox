package daemon

import (
	"errors"
	"strings"
	"testing"

	"agentbox/internal/agent"
)

// A project's chat is told whether a failed create_agent left an agent behind,
// so it doesn't try again and make a second one.
func TestCreateFailedNoteSaysWhatWasLeft(t *testing.T) {
	plain := createFailedNote(errors.New("choosing a name: database is locked"))
	if !strings.Contains(plain, "Nothing was created") {
		t.Errorf("a failure before anything was made reads %q", plain)
	}
	left := createFailedNote(&agent.LeftBehindError{Agent: "p/agent-07", Err: errors.New("creating p/agent-07: worktree: boom"), Undo: errors.New("database is locked")})
	if !strings.Contains(left, "p/agent-07 still exists") || strings.Contains(left, "Nothing was created") {
		t.Errorf("a failure that left the agent reads %q", left)
	}
}
