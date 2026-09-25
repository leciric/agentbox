package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// The chain the whole feature is about: an agent asks, the project's chat
// answers — or passes it to the user, who does. The agent waits either way.
func TestAgentAsksTheChatWhichAnswersOrEscalates(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	// The chat answers one itself, and the waiting agent gets it.
	asked := make(chan api.Question, 1)
	go func() {
		q, err := d.srv.askForTest(ctx, a, "Should the page paginate?", "building it")
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		asked <- q
	}()
	id := waitForQuestion(t, d, "hello-stack")
	if _, err := d.srv.answerQuestion(ctx, id, "Yes, 20 per page.", "lead"); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-asked:
		if answer.Answer != "Yes, 20 per page." || answer.AnsweredBy != "lead" {
			t.Errorf("the agent got %+v", answer)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never told the answer")
	}

	// One the chat can't answer goes to the user, who answers it instead.
	go func() {
		q, err := d.srv.askForTest(ctx, a, "Which payment provider?", "")
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		asked <- q
	}()
	id = waitForQuestion(t, d, "hello-stack")
	if _, err := d.srv.store.EscalateQuestion(ctx, id, "this is a product decision"); err != nil {
		t.Fatal(err)
	}
	// The user now sees it waiting for them.
	waiting, err := d.client.Questions(ctx, "hello-stack", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Status != state.QuestionEscalated {
		t.Fatalf("Questions() = %+v, want one escalated", waiting)
	}
	if waiting[0].Escalation != "this is a product decision" {
		t.Errorf("the user isn't told why: %q", waiting[0].Escalation)
	}
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", id, "Stripe."); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-asked:
		if answer.Answer != "Stripe." || answer.AnsweredBy != "user" {
			t.Errorf("the agent got %+v, want the user's answer", answer)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never told the user's answer")
	}

	// Nothing is left waiting.
	if left, _ := d.client.Questions(ctx, "hello-stack", false); len(left) != 0 {
		t.Errorf("still waiting: %+v", left)
	}
}

// Autonomy governs what the lead does once it notices something — act, or
// propose and wait — not whether it notices at all.
func TestAutonomyIsSetPerProjectAndDefaultsToAsk(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	// A new project proposes rather than acts.
	if p.Autonomy != state.AutonomyAsk {
		t.Errorf("a new project's autonomy = %q, want ask", p.Autonomy)
	}
	updated, err := d.client.SetAutonomy(ctx, "hello-stack", state.AutonomyOn)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Autonomy != state.AutonomyOn {
		t.Errorf("SetAutonomy() = %q, want on", updated.Autonomy)
	}
	if _, err := d.client.SetAutonomy(ctx, "hello-stack", "whenever"); err == nil {
		t.Error("an unknown autonomy was accepted")
	}
}

// TestAgentModelIsSetPerProject covers the project setting over the API: the
// three things it can say, and the one value that is not a model.
func TestAgentModelIsSetPerProject(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	// A new project follows the model new agents start on everywhere.
	if p.AgentModel != "" {
		t.Errorf("a new project's agent model = %q, want none", p.AgentModel)
	}
	for _, want := range []string{"haiku", api.AgentModelAuto, ""} {
		updated, err := d.client.SetAgentModel(ctx, "hello-stack", want)
		if err != nil {
			t.Fatalf("SetAgentModel(%q): %v", want, err)
		}
		if updated.AgentModel != want {
			t.Errorf("SetAgentModel(%q) = %q", want, updated.AgentModel)
		}
		stored, err := d.client.Project(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		if stored.AgentModel != want {
			t.Errorf("the project reads back as %q, want %q", stored.AgentModel, want)
		}
	}
	// "default" is the model menu's own word for the tool's own default.
	if _, err := d.client.SetAgentModel(ctx, "hello-stack", "default"); err == nil {
		t.Error(`"default" was accepted as a model`)
	}
}

// waitForQuestion waits for an agent's question to reach the store.
func waitForQuestion(t *testing.T, d testDaemon, project string) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		questions, err := d.srv.store.Questions(context.Background(), project, true)
		if err == nil && len(questions) == 1 {
			return questions[0].ID
		}
	}
	t.Fatal("no question arrived")
	return ""
}

func TestQuestionsAreScopedToTheirProject(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	// Answering a question that isn't there is refused, not silently ignored.
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", "nope", "anything"); err == nil {
		t.Error("answering a question that doesn't exist succeeded")
	}
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", "nope", ""); err == nil ||
		!strings.Contains(err.Error(), "no answer") {
		t.Errorf("an empty answer = %v, want it refused", err)
	}
}

// resolveFinishStartsTurn's table: FinishNoticesChat and FinishNoticesOff
// decide it for every agent of the project, whatever the agent chose;
// FinishNoticesLead leaves it to the agent, defaulting to true when it chose
// nothing.
func TestResolveFinishStartsTurn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		project, agent string
		want           bool
	}{
		{state.FinishNoticesChat, "", true},
		{state.FinishNoticesChat, state.FinishNoticesOff, true},
		{state.FinishNoticesChat, state.FinishNoticesChat, true},
		{state.FinishNoticesOff, "", false},
		{state.FinishNoticesOff, state.FinishNoticesChat, false},
		{state.FinishNoticesOff, state.FinishNoticesOff, false},
		{state.FinishNoticesLead, "", true},
		{state.FinishNoticesLead, state.FinishNoticesChat, true},
		{state.FinishNoticesLead, state.FinishNoticesOff, false},
	}
	for _, c := range cases {
		if got := resolveFinishStartsTurn(c.project, c.agent); got != c.want {
			t.Errorf("resolveFinishStartsTurn(%q, %q) = %v, want %v", c.project, c.agent, got, c.want)
		}
	}
}

// TestBranchPrefixIsSetPerProject covers the project setting over the API,
// and the brief preview that names the branch an agent made now would get.
func TestBranchPrefixIsSetPerProject(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	if p.BranchPrefix != "agentbox/" {
		t.Errorf("a new project's branch prefix = %q, want agentbox/", p.BranchPrefix)
	}
	for _, want := range []string{"thiago/agentbox/", ""} {
		updated, err := d.client.SetBranchPrefix(ctx, "hello-stack", want)
		if err != nil {
			t.Fatalf("SetBranchPrefix(%q): %v", want, err)
		}
		if updated.BranchPrefix != want {
			t.Errorf("SetBranchPrefix(%q) = %q", want, updated.BranchPrefix)
		}
	}
	if _, err := d.client.SetBranchPrefix(ctx, "hello-stack", "a..b/"); err == nil {
		t.Error("a prefix git refuses was accepted")
	}
	testutil.Git(t, repo, "branch", "thiago")
	if _, err := d.client.SetBranchPrefix(ctx, "hello-stack", "thiago/agentbox/"); err == nil || !strings.Contains(err.Error(), "branch thiago") {
		t.Errorf("a prefix under an existing branch: got %v, want it refused", err)
	}

	if _, err := d.client.SetBranchPrefix(ctx, "hello-stack", "mine/"); err != nil {
		t.Fatal(err)
	}
	brief, err := d.client.Brief(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "`mine/agent-01`") {
		t.Errorf("the brief preview doesn't name mine/agent-01:\n%s", brief)
	}
}
