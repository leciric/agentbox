package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

func TestCheckGoal(t *testing.T) {
	t.Parallel()
	s := &Server{}
	if _, err := s.checkGoal(&api.GoalSpec{Objective: "x"}); err == nil || !strings.Contains(err.Error(), "Experimental") {
		t.Errorf("a goal while goals are off: %v", err)
	}
	if g, err := s.checkGoal(nil); g != nil || err != nil {
		t.Errorf("no goal = %v, %v", g, err)
	}
	s.goalsOn.Store(true)
	for _, bad := range []api.GoalSpec{
		{Objective: "  "},
		{Objective: "x", Budget: -1},
		{Objective: "x", Budget: api.MaxGoalBudget + 1},
		{Objective: "x", Checks: []api.GoalCheck{{Kind: "tests_pass"}}},
		{Objective: "x", Checks: []api.GoalCheck{{Kind: api.GoalCheckCommand}}},
	} {
		if _, err := s.checkGoal(&bad); err == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
	g, err := s.checkGoal(&api.GoalSpec{Objective: " Ship ", Checks: []api.GoalCheck{{Kind: "ci_green", Command: "ignored"}, {Kind: "command", Command: " go test ./x "}}})
	if err != nil {
		t.Fatal(err)
	}
	if g.Objective != "Ship" || g.Budget != api.DefaultGoalBudget || g.Checks[0].Command != "" || g.Checks[1].Command != "go test ./x" {
		t.Errorf("goal = %+v", g)
	}
}

func TestGoalBlockedLine(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"I tried.\n\nGOAL BLOCKED: I need the staging password": "I need the staging password",
		"**GOAL BLOCKED:** no access":                           "** no access",
		"goal blocked: lower case too":                          "lower case too",
	} {
		m := goalBlockedLine.FindStringSubmatch(text)
		if m == nil || m[1] != want {
			t.Errorf("%q: %q, want %q", text, m, want)
		}
	}
	if goalBlockedLine.MatchString("The goal isn't blocked: GOAL BLOCKED would mean so.") {
		t.Error("matched mid-line")
	}
}

// An agent with a goal the daemon drives is sent back while a check fails,
// told what is missing, and blocked — asking, the lead told — once its budget
// is spent; no finish is reported meanwhile.
func TestAGoalSendsTheAgentBackUntilItsBudgetIsSpent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Pushed my work."}}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Open the PR")
	if err := d.srv.setGoalsFlag(ctx, true); err != nil {
		t.Fatal(err)
	}
	g := api.AgentGoal{Objective: "a PR is open", Source: api.GoalSourceAgentBox, Status: api.GoalActive, Budget: 2,
		Checks: []api.GoalCheck{{Kind: api.GoalCheckPROpen}}}
	if err := d.srv.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
		t.Fatal(err)
	}
	d.srv.leadAsked(a)
	if _, err := d.client.SendChat(ctx, a.Ref(), "Open the PR."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the goal to be blocked", func() bool {
		got := d.srv.goalOf(ctx, a.Project, a.Name)
		return got != nil && got.Status == api.GoalBlocked
	})
	if n := tool.got("your goal isn't met yet"); n != 2 {
		t.Errorf("sent back %d times, want 2", n)
	}
	if n := tool.got("no pull request from your branch yet"); n != 2 {
		t.Errorf("told what is missing %d times, want 2", n)
	}
	got := d.srv.goalOf(ctx, a.Project, a.Name)
	if got.Continuations != 2 || got.Checked != 3 || got.Checks[0].Met == nil || *got.Checks[0].Met {
		t.Errorf("goal = %+v", got)
	}
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeAsking || !strings.Contains(o.note, "Goal blocked") {
		t.Errorf("outcome = %+v, want asking on the goal", o)
	}
	if notice := noticeTo(t, d, lead); !strings.Contains(notice, "blocked on its goal") {
		t.Errorf("the lead was told %q", notice)
	}
	for _, k := range eventKinds(t, d, a.Project) {
		if k == api.AgentFinished {
			t.Error("a finish was reported for a goal not met")
		}
	}
	if info, err := d.client.Agent(ctx, a.Ref()); err != nil || info.Goal == nil || info.Goal.Status != api.GoalBlocked {
		t.Errorf("the API's agent has goal %+v (%v)", info.Goal, err)
	}
	// Off, the goal isn't the API's to show.
	if err := d.srv.setGoalsFlag(ctx, false); err != nil {
		t.Fatal(err)
	}
	if info, _ := d.client.Agent(ctx, a.Ref()); info.Goal != nil {
		t.Errorf("goal shown while goals are off: %+v", info.Goal)
	}
}

// An agent that says it is blocked is, at once; one whose goal has no checks
// meets it by ending its turn, and finishes as ever.
func TestAGoalBlockedByTheAgentOrMet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"I can't reach the database.\n\nGOAL BLOCKED: I need DATABASE_URL", "Done: the page works."}}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Build the page")
	if err := d.srv.setGoalsFlag(ctx, true); err != nil {
		t.Fatal(err)
	}
	set := func() {
		t.Helper()
		g := api.AgentGoal{Objective: "the page works", Source: api.GoalSourceAgentBox, Status: api.GoalActive, Budget: 5}
		if err := d.srv.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
			t.Fatal(err)
		}
	}
	set()
	if _, err := d.client.SendChat(ctx, a.Ref(), "Build the page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the goal to be blocked", func() bool {
		got := d.srv.goalOf(ctx, a.Project, a.Name)
		return got != nil && got.Status == api.GoalBlocked
	})
	if got := d.srv.goalOf(ctx, a.Project, a.Name); got.LastUnmet != "The agent says: I need DATABASE_URL" || got.Continuations != 0 {
		t.Errorf("goal = %+v", got)
	}
	set()
	if _, err := d.client.SendChat(ctx, a.Ref(), "Here it is."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the goal to be met", func() bool {
		got := d.srv.goalOf(ctx, a.Project, a.Name)
		return got != nil && got.Status == api.GoalMet
	})
	waitFor(t, "the finish", func() bool {
		for _, k := range eventKinds(t, d, a.Project) {
			if k == api.AgentFinished {
				return true
			}
		}
		return false
	})
}
