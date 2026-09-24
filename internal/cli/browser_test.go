package cli

import (
	"os"
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestBrowserNeedsAnAgentOutsideAgents(t *testing.T) {
	if _, err := os.Stat(api.InAgentSocket); err == nil {
		t.Skip("running inside an agent")
	}
	for _, args := range [][]string{{"browser", "status"}, {"browser", "start"}, {"browser", "open", "http://localhost:3000"}} {
		cmd := NewRootCmd()
		cmd.SetArgs(args)
		cmd.SetOut(new(strings.Builder))
		cmd.SetErr(new(strings.Builder))
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "name an agent") {
			t.Errorf("agentbox %s outside an agent: got %v, want a request to name an agent", strings.Join(args, " "), err)
		}
	}
}
