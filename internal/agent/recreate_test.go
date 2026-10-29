package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// recreateAgent adds an agent with a real worktree of the fixture's project.
func recreateAgent(t *testing.T, f fixture, status string) state.Agent {
	t.Helper()
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "none",
		Branch: "agentbox/agent-01", BaseRef: "main", Worktree: filepath.Join(t.TempDir(), "agent-01"), Status: status, CreatedAt: time.Now()}
	commit, err := f.repo.ResolveCommit("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.AddWorktree(a.Worktree, a.Branch, commit, "agentbox: "+a.Ref()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Worktree, "uncommitted.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// An agent whose machine is there is left alone: Recreate is for one whose
// machine is gone.
func TestRecreateLeavesAnAgentWithItsMachine(t *testing.T) {
	f := setup(t, fakeIncus(t, `[ "$1" = list ] && echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]'; exit 0`))
	a := recreateAgent(t, f, state.AgentReady)
	err := f.m.Recreate(context.Background(), a, agent.RecreateOptions{})
	if !errors.Is(err, agent.ErrHasMachine) {
		t.Fatalf("Recreate = %v, want ErrHasMachine", err)
	}
}

// A machine an interrupted Recreate left (the agent still marked creating) is
// removed and made again; a failure on the way removes the new one and puts
// the agent back as it was, its worktree and uncommitted work untouched.
func TestRecreateReplacesAHalfMadeMachineAndRollsBack(t *testing.T) {
	log := filepath.Join(t.TempDir(), "incus.log")
	t.Setenv("INCUS_LOG", log)
	f := setup(t, fakeIncus(t, `echo "$*" >> "$INCUS_LOG"
case "$1" in
  list) [ -e "$INCUS_LOG.deleted" ] && echo '[]' || echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  delete) touch "$INCUS_LOG.deleted" ;;
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: simulated copy failure" >&2; exit 1 ;;
esac
exit 0`))
	a := recreateAgent(t, f, state.AgentCreating)
	ctx := context.Background()
	err := f.m.Recreate(ctx, a, agent.RecreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "simulated copy failure") {
		t.Fatalf("Recreate = %v, want the copy's failure", err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "delete --force ab-hello-stack-agent-01") {
		t.Errorf("the half-made machine wasn't removed:\n%s", calls)
	}
	got, err := f.st.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatalf("the agent's record: %v", err)
	}
	if got.Status != state.AgentReady {
		t.Errorf("status after a failed Recreate = %q, want it back to ready, to try again", got.Status)
	}
	if data, err := os.ReadFile(filepath.Join(a.Worktree, "uncommitted.txt")); err != nil || string(data) != "work in progress" {
		t.Errorf("the worktree's uncommitted work: %q, %v", data, err)
	}
	if !f.repo.HasWorktree(a.Worktree) {
		t.Error("the failed Recreate removed the agent's worktree")
	}
}

// TestRecreateUpdatesALegacyOpencodeProvider: an agent row from before the Go
// switch says opencode=h4ad while the store now holds that account under
// opencode-go. Recreating it resolves the provider and writes the corrected
// row, so its machine gets the opencode-go auth instead of failing.
func TestRecreateUpdatesALegacyOpencodeProvider(t *testing.T) {
	log := filepath.Join(t.TempDir(), "incus.log")
	t.Setenv("INCUS_LOG", log)
	f := setup(t, fakeIncus(t, `case "$1" in
  list) if [ -e "$INCUS_LOG.copied" ]; then echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]'; else echo '[]'; fi ;;
  copy) touch "$INCUS_LOG.copied" ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {"user.agentbox.with-opencode": "1"}, "devices": {}}' ;;
    esac ;;
  delete) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0`))
	if err := f.m.Creds.SaveToolAccount("opencode", credentials.OpenCodeGoProvider, "h4ad", []byte(`{"type":"api","key":"sk"}`)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "opencode",
		OpenCodeAccounts: state.AccountMap{"opencode": "h4ad"},
		Branch:           "agentbox/agent-01", BaseRef: "main", Worktree: filepath.Join(t.TempDir(), "agent-01"),
		Status: state.AgentReady, CreatedAt: time.Now()}
	commit, err := f.repo.ResolveCommit("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.AddWorktree(a.Worktree, a.Branch, commit, "agentbox: "+a.Ref()); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Recreate(ctx, a, agent.RecreateOptions{Stopped: true}); err != nil {
		t.Fatalf("Recreate of a legacy OpenCode agent: %v", err)
	}
	got, err := f.st.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.OpenCodeAccounts[credentials.OpenCodeGoProvider] != "h4ad" || got.OpenCodeAccounts["opencode"] != "" {
		t.Errorf("the agent's OpenCode accounts = %v, want opencode-go=h4ad", got.OpenCodeAccounts)
	}
}

func TestRecreateRefusesWhatHasNoMachineToMake(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in list|query) echo '[]' ;; esac; exit 0`))
	ctx := context.Background()
	lead := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead}
	if err := f.m.Recreate(ctx, lead, agent.RecreateOptions{}); err == nil || !strings.Contains(err.Error(), "no machine") {
		t.Errorf("Recreate of a lead = %v", err)
	}
	gone := state.Agent{Project: "hello-stack", Name: "agent-09", Instance: "ab-hello-stack-agent-09", Worktree: filepath.Join(t.TempDir(), "gone"), Status: state.AgentReady}
	if err := f.m.Recreate(ctx, gone, agent.RecreateOptions{}); err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Errorf("Recreate without a worktree = %v", err)
	}
	a := recreateAgent(t, f, state.AgentReady)
	if err := f.m.Recreate(ctx, a, agent.RecreateOptions{}); err == nil || !strings.Contains(err.Error(), "base image isn't built") {
		t.Errorf("Recreate without a base image = %v", err)
	}
}
