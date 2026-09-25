package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

func TestLimitsDescribeSummarizesTheCaps(t *testing.T) {
	cases := []struct {
		limits agent.Limits
		want   string
	}{
		{agent.Limits{}, "every core, no memory limit"},
		{agent.Limits{CPU: "1"}, "1 core, no memory limit"},
		{agent.Limits{CPU: "4"}, "4 cores, no memory limit"},
		{agent.Limits{Memory: "4GB"}, "every core, 4GB of memory"},
		{agent.Limits{CPU: "2", Memory: "8GB", Allowance: "50%"}, "2 cores, 8GB of memory, a CPU share of 50%"},
	}
	for _, c := range cases {
		if got := c.limits.Describe(); got != c.want {
			t.Errorf("Describe(%+v) = %q, want %q", c.limits, got, c.want)
		}
	}
}

func TestLeadRefNamesTheProjectsLead(t *testing.T) {
	if got, want := agent.LeadRef("pawly"), "pawly/"+state.LeadName; got != want {
		t.Errorf("LeadRef(pawly) = %q, want %q", got, want)
	}
}

// TestLeadPromptCacheTTLReadsTheLeadsOwnSettings checks that it reads the
// project's lead's settings.json under LeadHome, and is 0 rather than an
// error when the lead has never started (no file yet).
func TestLeadPromptCacheTTLReadsTheLeadsOwnSettings(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead}

	if got := f.m.LeadPromptCacheTTL(a); got != 0 {
		t.Errorf("LeadPromptCacheTTL() with no settings.json yet = %s, want 0", got)
	}

	settingsPath := filepath.Join(f.m.Paths.LeadHome("hello-stack"), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"promptCacheTtl": "1h"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := f.m.LeadPromptCacheTTL(a), time.Hour; got != want {
		t.Errorf("LeadPromptCacheTTL() = %s, want %s", got, want)
	}
}

func TestApplyThemeRequiresTheAgentToBeRunning(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in list) echo '[]' ;; esac`))
	a := state.Agent{Instance: "ab-hello-stack-agent-01"}
	if err := f.m.ApplyTheme(context.Background(), a); err == nil {
		t.Error("ApplyTheme() on a missing instance should fail")
	}
}

// TestApplyThemeRepaintsARunningAgentsDesktop checks that a running agent's
// theme is installed and its browser script re-run with "theme", the whole
// point of ApplyTheme.
func TestApplyThemeRepaintsARunningAgentsDesktop(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo "$*" >>`+calls+` ;;
esac`))
	a := state.Agent{Instance: "ab-hello-stack-agent-01"}
	if err := f.m.ApplyTheme(context.Background(), a); err != nil {
		t.Fatalf("ApplyTheme() = %v", err)
	}
	out, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(out), "agentbox-browser theme") {
		t.Errorf("ApplyTheme() didn't run the browser script's theme action:\n%s", out)
	}
}

// TestUsageSamplesTheHostAndItsAgentsTwice checks that Usage reports every
// stored agent, including one whose instance has vanished (State "missing"),
// and reads the storage pool's used and total bytes off the host's default
// pool.
func TestUsageSamplesTheHostAndItsAgentsTwice(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"cpu":{"usage":1000000000},"memory":{"usage":1048576},"processes":3}}]' ;;
  query) echo '{"space":{"used":500,"total":1000}}' ;;
esac`))
	ctx := context.Background()
	running := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	missing := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := f.st.AddAgent(ctx, running); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddAgent(ctx, missing); err != nil {
		t.Fatal(err)
	}

	host, agents, err := f.m.Usage(ctx, time.Millisecond)
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}
	if host.PoolUsed != 500 || host.PoolTotal != 1000 {
		t.Errorf("Usage() host pool = %d/%d, want 500/1000", host.PoolUsed, host.PoolTotal)
	}
	if host.Cores < 1 {
		t.Errorf("Usage() host cores = %d, want at least 1", host.Cores)
	}
	byName := map[string]agent.AgentUsage{}
	for _, u := range agents {
		byName[u.Name] = u
	}
	if byName["agent-01"].State != "running" || byName["agent-01"].Processes != 3 {
		t.Errorf("Usage() agent-01 = %+v, want it running with 3 processes", byName["agent-01"])
	}
	if byName["agent-02"].State != "missing" {
		t.Errorf("Usage() agent-02 = %+v, want it missing", byName["agent-02"])
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
