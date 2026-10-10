package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// TestAnAgentChangingBalancesTheCPU: once an agent changes, the daemon gives
// every running agent its share of the cores, and leaves a stopped one alone.
func TestAnAgentChangingBalancesTheCPU(t *testing.T) {
	cores := runtime.NumCPU()
	share := agent.CPUShare(cores, 2)
	if share >= cores {
		t.Skipf("with %d cores, two agents each have all of them", cores)
	}
	root := t.TempDir()
	instance := func(name, status string) string {
		return fmt.Sprintf(`{"name":%q,"status":%q,"config":{},"expanded_config":{}}`, name, status)
	}
	d := startTestDaemon(t, root, statefulIncus, testConfig{
		instances: "[" + instance("ab-p-a1", "Running") + "," + instance("ab-p-a2", "Running") + "," + instance("ab-p-a3", "Stopped") + "]",
	})
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a1", "a2", "a3"} {
		if err := d.srv.store.AddAgent(ctx, state.Agent{
			Project: "p", Name: name, Instance: "ab-p-" + name, AI: "none",
			Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	d.srv.refreshAgents(ctx)
	log := func() string {
		b, _ := os.ReadFile(filepath.Join(root, "incus.log"))
		return string(b)
	}
	waitFor(t, "both running agents to get their share", func() bool {
		return strings.Contains(log(), fmt.Sprintf("config set ab-p-a1 limits.cpu=%d", share)) &&
			strings.Contains(log(), fmt.Sprintf("config set ab-p-a2 limits.cpu=%d", share))
	})
	if strings.Contains(log(), "ab-p-a3") {
		t.Errorf("the stopped agent was changed:\n%s", log())
	}
}
