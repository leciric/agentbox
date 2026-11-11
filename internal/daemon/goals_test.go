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
	if g, err := checkGoal(nil); g != nil || err != nil {
		t.Errorf("no goal = %v, %v", g, err)
	}
	for _, bad := range []api.GoalSpec{
		{Objective: "  "},
		{Objective: "x", Budget: -1},
		{Objective: "x", Budget: api.MaxGoalBudget + 1},
		{Objective: "x", Checks: []api.GoalCheck{{Kind: "tests_pass"}}},
		{Objective: "x", Checks: []api.GoalCheck{{Kind: api.GoalCheckCommand}}},
	} {
		if _, err := checkGoal(&bad); err == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
	g, err := checkGoal(&api.GoalSpec{Objective: " Ship ", Checks: []api.GoalCheck{{Kind: "ci_green", Command: "ignored"}, {Kind: "command", Command: " go test ./x "}}})
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

// A goal set on an agent that already exists, by the user's API and by the
// lead's: an idle agent starts a turn on it and is held to it like one
// create_agent gave; a new goal starts afresh; a clear stops it.
func TestAGoalSetOnARunningAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Working on it."}}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Open the PR")

	if _, err := d.client.SetGoal(ctx, a.Ref(), api.GoalSpec{Objective: "  "}); err == nil {
		t.Error("a goal with no objective was taken")
	}
	if _, err := d.client.ClearGoal(ctx, a.Ref()); err == nil {
		t.Error("an agent with no goal had one cleared")
	}

	res, err := d.client.SetGoal(ctx, a.Ref(), api.GoalSpec{Objective: "a PR is open", Checks: []api.GoalCheck{{Kind: api.GoalCheckPROpen}}, Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Told != "turn" || res.Goal.Status != api.GoalActive || res.Goal.Budget != 1 || res.Goal.Source != api.GoalSourceAgentBox {
		t.Errorf("result = %+v", res)
	}
	waitFor(t, "the goal to be blocked", func() bool {
		got := d.srv.goalOf(ctx, a.Project, a.Name)
		return got != nil && got.Status == api.GoalBlocked
	})
	if n := tool.got("you have a goal: a PR is open"); n != 1 {
		t.Errorf("told the goal %d times, want 1", n)
	}
	if n := tool.got("your goal isn't met yet"); n != 1 {
		t.Errorf("sent back %d times, want 1", n)
	}

	// The lead gives it another, with no checks: met when its turn ends,
	// and the lead hears it finish.
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	res, err = lead.SetAgentGoal(ctx, a.Name, api.GoalSpec{Objective: "the page works"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Goal.Continuations != 0 || res.Goal.Budget != api.DefaultGoalBudget || res.Goal.BudgetLeft() != api.DefaultGoalBudget {
		t.Errorf("a new goal didn't start afresh: %+v", res.Goal)
	}
	waitFor(t, "the new goal to be met", func() bool {
		got := d.srv.goalOf(ctx, a.Project, a.Name)
		return got != nil && got.Objective == "the page works" && got.Status == api.GoalMet
	})
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind == api.AgentOutcomeAsking {
		t.Errorf("still asking after a new goal was met: %+v", o)
	}

	res, err = lead.ClearAgentGoal(ctx, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goal.Status != api.GoalCleared {
		t.Errorf("cleared goal = %+v", res.Goal)
	}
	if info, err := d.client.Agent(ctx, a.Ref()); err != nil || info.Goal == nil || info.Goal.Status != api.GoalCleared {
		t.Errorf("the API's agent has goal %+v (%v)", info.Goal, err)
	}
	if _, err := lead.ClearAgentGoal(ctx, a.Name); err == nil {
		t.Error("a cleared goal was cleared again")
	}
	var rows []string
	th, err := d.client.Chat(ctx, a.Ref())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range th.Items {
		if it.Kind == "goal" {
			rows = append(rows, it.Goal.Status)
		}
	}
	if got := strings.Join(rows, ","); !strings.HasPrefix(got, "active,") || !strings.HasSuffix(got, ",met,cleared") {
		t.Errorf("goal rows = %s", got)
	}
}

// A tool's own active /goal is the tool's to clear.
func TestAToolsOwnGoalIsntClearedByAgentBox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := restartDaemon(t, t.TempDir(), "Running", (&sayTool{replies: []string{"ok"}}).launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "x")
	g := api.AgentGoal{Objective: "ship", Source: api.GoalSourceTool, Status: api.GoalActive}
	if err := d.srv.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.ClearGoal(ctx, a.Ref()); err == nil || !strings.Contains(err.Error(), "/goal clear") {
		t.Errorf("clearing a tool's goal: %v", err)
	}
}
