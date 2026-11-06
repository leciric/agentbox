package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

func TestCheckGoal(t *testing.T) {
	t.Parallel()
	s := &Server{}
	if g, err := s.checkGoal(nil); g != nil || err != nil {
		t.Errorf("no goal = %v, %v", g, err)
	}
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

// A goal set on an agent that has finished sends it back to work towards it,
// and is checked when that turn ends; changing it starts it again, and
// clearing it stops it being checked. The lead sets one through its own
// socket.
func TestSettingAGoalOnARunningAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Built the page.", "Opened nothing yet."}}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Build the page")
	if _, err := d.client.SetAgentGoal(ctx, a.Ref(), api.GoalSpec{Objective: " "}); err == nil {
		t.Error("a goal with no objective was taken")
	}
	if got, err := d.client.ClearAgentGoal(ctx, a.Ref()); err != nil || got.Goal != nil {
		t.Errorf("clearing no goal = %+v, %v", got, err)
	}

	// Set: the agent, idle, is told its goal and works towards it; with no
	// checks, ending that turn meets it.
	got, err := d.client.SetAgentGoal(ctx, a.Ref(), api.GoalSpec{Objective: "the page works"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Goal == nil || got.Goal.Status != api.GoalActive || got.Goal.Source != api.GoalSourceAgentBox || got.Goal.Budget != api.DefaultGoalBudget {
		t.Fatalf("set = %+v", got.Goal)
	}
	waitFor(t, "the goal to be met", func() bool {
		g := d.srv.goalOf(ctx, a.Project, a.Name)
		return g != nil && g.Status == api.GoalMet
	})
	if n := tool.got("you have a goal: the page works"); n != 1 {
		t.Errorf("told its goal %d times, want 1", n)
	}

	// The lead changes it: a check that fails sends it back once, then it
	// is blocked.
	lead, err := d.client.NewLeadSession(ctx, "hello-stack", "Goals")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "/v1/project/agents/agent-01/goal",
		strings.NewReader(`{"objective":"a PR is open","checks":[{"kind":"pr_open"}],"budget":1}`))
	rec := httptest.NewRecorder()
	d.srv.leadRoutes("hello-stack", lead.ID).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the lead's set_goal = %d %s", rec.Code, rec.Body)
	}
	waitFor(t, "the changed goal to be blocked", func() bool {
		g := d.srv.goalOf(ctx, a.Project, a.Name)
		return g != nil && g.Status == api.GoalBlocked && g.Objective == "a PR is open"
	})
	if g := d.srv.goalOf(ctx, a.Project, a.Name); g.Continuations != 1 || len(g.Checks) != 1 {
		t.Errorf("changed goal = %+v", g)
	}
	if n := tool.got("you have a goal: a PR is open"); n != 1 {
		t.Errorf("told the new goal %d times, want 1", n)
	}

	// Cleared: it stays, as cleared, and isn't checked any more.
	cleared, err := d.client.ClearAgentGoal(ctx, a.Ref())
	if err != nil || cleared.Goal == nil || cleared.Goal.Status != api.GoalCleared {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
	if info, err := d.client.Agent(ctx, a.Ref()); err != nil || info.Goal == nil || info.Goal.Status != api.GoalCleared {
		t.Errorf("the API's agent has goal %+v (%v)", info.Goal, err)
	}
	if done := d.srv.goalAfterTurn(ctx, a, chat.TurnEnd{LastMessage: "More."}); done {
		t.Error("a cleared goal still drove the agent")
	}
	thread, err := d.client.Chat(ctx, a.Ref())
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, it := range thread.Items {
		if it.Kind == "goal" {
			rows = append(rows, it.Text)
		}
	}
	if len(rows) < 4 || rows[0] != "Goal set: the page works" || rows[len(rows)-1] != "Goal cleared: a PR is open" {
		t.Errorf("goal rows = %q", rows)
	}
}
