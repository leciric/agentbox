package cli_test

import (
	"strings"
	"testing"
)

// TestMoveNeedsAnAgentAndACluster: the command is registered and refuses before
// it reaches a daemon when it has nothing to move or nowhere to move it to.
func TestMoveNeedsAnAgentAndACluster(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "move"); err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Errorf("move with no agent = %v, want it to need one", err)
	}
	if _, err := run(t, "", "move", "pawly/agent-01"); err == nil || !strings.Contains(err.Error(), `required flag(s) "cluster"`) {
		t.Errorf("move with no cluster = %v, want it to need one", err)
	}
}
