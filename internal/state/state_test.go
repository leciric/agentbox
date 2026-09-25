package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

func open(t *testing.T, path string) *state.Store {
	t.Helper()
	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestProjects(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))

	p := state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Unix(1700000000, 0)}
	if err := st.AddProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/other"}); !errors.Is(err, state.ErrExists) {
		t.Errorf("duplicate name: got %v, want ErrExists", err)
	}
	if err := st.AddProject(ctx, state.Project{Name: "other", Root: "/src/pawly"}); !errors.Is(err, state.ErrExists) {
		t.Errorf("duplicate root: got %v, want ErrExists", err)
	}

	got, err := st.Project(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	p.Autonomy = state.AutonomyAsk                         // filled in by AddProject
	p.MediaRetentionDays = state.DefaultMediaRetentionDays // filled in by AddProject
	p.FinishNotices = state.FinishNoticesLead              // filled in by AddProject
	p.RolloverThreshold = state.DefaultRolloverThreshold   // filled in by AddProject
	p.ContextBudget = state.DefaultContextBudget           // filled in by AddProject
	p.Consolidation = state.DefaultConsolidation           // filled in by AddProject
	p.ConsolidationModel = state.DefaultConsolidationModel // filled in by AddProject
	p.BranchPrefix = state.DefaultBranchPrefix             // filled in by AddProject
	if !reflect.DeepEqual(got, p) {
		t.Errorf("Project() = %+v, want %+v", got, p)
	}

	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Project(ctx, "pawly"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("after remove: got %v, want ErrNotFound", err)
	}
	if err := st.RemoveProject(ctx, "pawly"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("remove missing: got %v, want ErrNotFound", err)
	}
}

func TestAgents(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	a := state.Agent{
		Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01",
		AI: "claude", Autonomous: true,
		Branch: "agentbox/agent-01", BaseRef: "main", BaseCommit: "abc123",
		Worktree: "/data/worktrees/pawly/agent-01",
		Status:   state.AgentCreating, CreatedAt: time.Unix(1700000000, 0),
		Source: "agentbox-base/ready", Interface: state.InterfaceChat,
	}
	if err := st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAgent(ctx, a); !errors.Is(err, state.ErrExists) {
		t.Errorf("duplicate agent: got %v, want ErrExists", err)
	}
	if err := st.SetAgentStatus(ctx, "pawly", "agent-01", state.AgentReady); err != nil {
		t.Fatal(err)
	}

	got, err := st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	want := a
	want.Status = state.AgentReady
	want.Role = state.RoleWorker // filled in by AddAgent
	if got != want {
		t.Errorf("Agent() = %+v, want %+v", got, want)
	}
	if got.Ref() != "pawly/agent-01" {
		t.Errorf("Ref() = %q", got.Ref())
	}

	for project, n := range map[string]int{"pawly": 1, "": 1, "other": 0} {
		if agents, err := st.Agents(ctx, project); err != nil || len(agents) != n {
			t.Errorf("Agents(%q) = %d agents, %v; want %d", project, len(agents), err, n)
		}
	}

	if err := st.RemoveProject(ctx, "pawly"); err == nil || !strings.Contains(err.Error(), "still has 1 agent") {
		t.Errorf("removing a project with agents: got %v", err)
	}
	if err := st.RemoveAgent(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agent(ctx, "pawly", "agent-01"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("after remove: got %v, want ErrNotFound", err)
	}
	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Errorf("removing a project without agents: %v", err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")

	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProject(ctx, state.Project{Name: "a", Root: "/a", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	projects, err := open(t, path).Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "a" {
		t.Errorf("Projects() after reopen = %+v", projects)
	}
}

// A project's lead is an agent row with no machine, and the name is reserved.
func TestLeadAgent(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	lead := state.Agent{
		Project: "pawly", Name: state.LeadName, Instance: "ab-pawly-lead",
		AI: "claude", BaseRef: "main", BaseCommit: "abc123",
		Worktree: "/data/worktrees/pawly/lead", Status: state.AgentReady,
		CreatedAt: time.Unix(1700000000, 0), Interface: state.InterfaceChat,
		Role: state.RoleLead,
	}
	if err := st.AddAgent(ctx, lead); err != nil {
		t.Fatal(err)
	}
	got, err := st.Agent(ctx, "pawly", state.LeadName)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsLead() {
		t.Errorf("IsLead() = false for %+v", got)
	}
	if got.Branch != "" {
		t.Errorf("Branch = %q, want empty: the lead commits nothing", got.Branch)
	}
	// It moves as the branch it stands on moves; an ordinary agent never does.
	if err := st.SetAgentBaseCommit(ctx, "pawly", state.LeadName, "def456"); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.Agent(ctx, "pawly", state.LeadName); got.BaseCommit != "def456" {
		t.Errorf("BaseCommit = %q, want def456", got.BaseCommit)
	}
	// An ordinary agent of the same project is not a lead.
	worker := lead
	worker.Name, worker.Instance, worker.Role = "agent-01", "ab-pawly-agent-01", ""
	if err := st.AddAgent(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Agent(ctx, "pawly", "agent-01"); got.IsLead() {
		t.Errorf("IsLead() = true for an ordinary agent")
	}
}

// The context budget's column default and package memory's constant are the
// same number written twice — a migration is SQL and can't name a Go constant
// — so a project made before the column existed gets what a new one gets.
func TestContextBudgetDefaultMatchesMemory(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly"}); err != nil {
		t.Fatal(err)
	}
	// Straight to the column, past AddProject's own defaulting, which is what
	// an older project's row went through.
	if _, err := st.DB().ExecContext(ctx, `UPDATE projects SET context_budget = (SELECT dflt_value FROM pragma_table_info('projects') WHERE name = 'context_budget')`); err != nil {
		t.Fatal(err)
	}
	p, err := st.Project(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if p.ContextBudget != memory.DefaultBudgetTokens {
		t.Errorf("the context_budget column defaults to %d, but memory.DefaultBudgetTokens is %d",
			p.ContextBudget, memory.DefaultBudgetTokens)
	}
}

func TestSetProjectBranchPrefix(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly"}); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"thiago/agentbox/", ""} {
		if err := st.SetProjectBranchPrefix(ctx, "pawly", prefix); err != nil {
			t.Fatalf("SetProjectBranchPrefix(%q): %v", prefix, err)
		}
		if p, _ := st.Project(ctx, "pawly"); p.BranchPrefix != prefix {
			t.Errorf("after SetProjectBranchPrefix(%q): BranchPrefix = %q", prefix, p.BranchPrefix)
		}
	}
	if err := st.SetProjectBranchPrefix(ctx, "pawly", "a..b/"); err == nil {
		t.Error("SetProjectBranchPrefix(a..b/) succeeded")
	}
	if err := st.SetProjectBranchPrefix(ctx, "nope", "x/"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown project: got %v, want ErrNotFound", err)
	}
}
