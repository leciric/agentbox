package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

// The chain: an agent asks, the chat answers — or passes it to the user.
func TestQuestions(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ask := func(id, text string) state.Question {
		q := state.Question{ID: id, Project: "pawly", Agent: "agent-01", Text: text,
			Status: state.QuestionPending, CreatedAt: time.Now()}
		if err := st.AddQuestion(ctx, q); err != nil {
			t.Fatal(err)
		}
		return q
	}

	// The chat answers one itself.
	ask("q1", "Should the page paginate?")
	answered, err := st.AnswerQuestion(ctx, "q1", "Yes, 20 per page.", "lead")
	if err != nil {
		t.Fatal(err)
	}
	if answered.Status != state.QuestionAnswered || answered.AnsweredBy != "lead" || answered.Waiting() {
		t.Errorf("AnswerQuestion() = %+v", answered)
	}
	if answered.AnsweredAt.IsZero() {
		t.Error("an answered question has no time on it")
	}
	// Answering twice is refused, so two answers can't race.
	if _, err := st.AnswerQuestion(ctx, "q1", "No", "user"); err == nil {
		t.Error("a second answer was accepted")
	}

	// One it can't: it goes to the user, who answers.
	ask("q2", "Which payment provider?")
	escalated, err := st.EscalateQuestion(ctx, "q2", "this is a product decision")
	if err != nil {
		t.Fatal(err)
	}
	if escalated.Status != state.QuestionEscalated || !escalated.Waiting() {
		t.Errorf("EscalateQuestion() = %+v", escalated)
	}
	if escalated.Escalation != "this is a product decision" {
		t.Errorf("the reason was lost: %q", escalated.Escalation)
	}
	byUser, err := st.AnswerQuestion(ctx, "q2", "Stripe.", "user")
	if err != nil {
		t.Fatal(err)
	}
	if byUser.AnsweredBy != "user" {
		t.Errorf("AnsweredBy = %q, want user", byUser.AnsweredBy)
	}
	// An answered question can't be escalated afterwards.
	if _, err := st.EscalateQuestion(ctx, "q2", "too late"); err == nil {
		t.Error("an answered question was escalated")
	}

	// Waiting lists only what somebody still has to deal with.
	ask("q3", "Still waiting")
	waiting, err := st.Questions(ctx, "pawly", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].ID != "q3" {
		t.Errorf("Questions(waiting) = %+v, want only q3", waiting)
	}
	if all, _ := st.Questions(ctx, "pawly", false); len(all) != 3 {
		t.Errorf("Questions(all) = %d, want 3", len(all))
	}

	// Retiring an agent gives up on its unanswered questions.
	if err := st.CancelQuestions(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if waiting, _ := st.Questions(ctx, "pawly", true); len(waiting) != 0 {
		t.Errorf("Questions(waiting) = %+v after cancelling", waiting)
	}
}

func TestProjectAutonomy(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// A project proposes rather than acts until you say otherwise.
	if p, _ := st.Project(ctx, "pawly"); p.Autonomy != state.AutonomyAsk {
		t.Errorf("a new project's autonomy = %q, want %q", p.Autonomy, state.AutonomyAsk)
	}
	if err := st.SetProjectAutonomy(ctx, "pawly", state.AutonomyOn); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Project(ctx, "pawly"); p.Autonomy != state.AutonomyOn {
		t.Errorf("autonomy = %q, want on", p.Autonomy)
	}
	if err := st.SetProjectAutonomy(ctx, "pawly", "whenever-it-likes"); err == nil {
		t.Error("an unknown autonomy was accepted")
	}
}

// A project says what a finishing agent does to its chat, and a new one
// defaults to letting the agent that finished decide.
func TestProjectFinishNotices(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Project(ctx, "pawly"); p.FinishNotices != state.FinishNoticesLead {
		t.Errorf("a new project's finish notices = %q, want %q", p.FinishNotices, state.FinishNoticesLead)
	}
	if err := st.SetProjectFinishNotices(ctx, "pawly", state.FinishNoticesOff); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Project(ctx, "pawly"); p.FinishNotices != state.FinishNoticesOff {
		t.Errorf("finish notices = %q, want off", p.FinishNotices)
	}
	// It survives being listed as well as read one at a time.
	projects, err := st.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].FinishNotices != state.FinishNoticesOff {
		t.Errorf("Projects() = %+v, want one with finish notices off", projects)
	}
	if err := st.SetProjectFinishNotices(ctx, "pawly", state.FinishNoticesLead); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Project(ctx, "pawly"); p.FinishNotices != state.FinishNoticesLead {
		t.Errorf("finish notices = %q, want lead", p.FinishNotices)
	}
	if err := st.SetProjectFinishNotices(ctx, "pawly", "quietly"); err == nil {
		t.Error("an unknown finish-notices value was accepted")
	}
	if err := st.SetProjectFinishNotices(ctx, "no-such-project", state.FinishNoticesOff); err == nil {
		t.Error("setting finish notices on a project that doesn't exist succeeded")
	}
}

// TestProjectAgentModel covers the three things a project's AgentModel can
// say, and what each one means to the code that reads it.
func TestProjectAgentModel(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// A project names no model until you set one: the installation's setting
	// decides, which is what every project did before this existed.
	p, _ := st.Project(ctx, "pawly")
	if p.AgentModel != "" || p.DirectAgentModel() != "" || p.LeadPicksModel() {
		t.Errorf("a new project's agent model = %q (direct %q, lead picks %v), want none of the three",
			p.AgentModel, p.DirectAgentModel(), p.LeadPicksModel())
	}

	if err := st.SetProjectAgentModel(ctx, "pawly", "haiku"); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Project(ctx, "pawly")
	if p.DirectAgentModel() != "haiku" || p.LeadPicksModel() {
		t.Errorf("with a model named: direct = %q, lead picks = %v, want haiku and false", p.DirectAgentModel(), p.LeadPicksModel())
	}

	// Auto is not a model: the chat chooses per agent, so nothing here names
	// one and an agent chosen for lands back on the installation's setting.
	if err := st.SetProjectAgentModel(ctx, "pawly", state.AgentModelAuto); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Project(ctx, "pawly")
	if !p.LeadPicksModel() || p.DirectAgentModel() != "" {
		t.Errorf("on auto: direct = %q, lead picks = %v, want no model and true", p.DirectAgentModel(), p.LeadPicksModel())
	}

	// "default" is the menu's own word for "no model of my own". Stored as a
	// model id, every turn of every agent of the project would fail.
	if err := st.SetProjectAgentModel(ctx, "pawly", "default"); err == nil {
		t.Error(`"default" was accepted as a model`)
	}
	if err := st.SetProjectAgentModel(ctx, "nowhere", "haiku"); err == nil {
		t.Error("a model was set on a project that doesn't exist")
	}

	// And back to following the installation's setting.
	if err := st.SetProjectAgentModel(ctx, "pawly", ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Project(ctx, "pawly"); p.AgentModel != "" {
		t.Errorf("agent model = %q after clearing it", p.AgentModel)
	}
}
