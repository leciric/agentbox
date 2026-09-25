package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

var eventTime = time.Date(2026, 9, 20, 11, 30, 0, 0, time.UTC)

func worker(name, title string) state.Agent {
	return state.Agent{Project: "hello-stack", Name: name, Title: title}
}

// A finish is the event the thread is built around: what the agent changed,
// the pull request it opened, and what it said about the work — each as
// itself, not as a sentence the app would have to read back.
func TestFinishedEventCarriesTheWorkAsFields(t *testing.T) {
	summary := "Added pagination to the reminders list, twenty a page, with the page in the query string."
	changes := api.AgentChanges{Files: 4, Insertions: 120, Deletions: 8, Dirty: true}
	pr := &api.PullRequest{Number: 58, Title: "Paginate the reminders", URL: "https://github.com/leciric/agentbox/pull/58"}

	ev := finishedEvent(worker("agent-01", "Reminders page"), changes, pr, summary, eventTime)

	if ev.Kind != api.AgentFinished {
		t.Errorf("kind = %q, want %q", ev.Kind, api.AgentFinished)
	}
	if ev.Ref != "hello-stack/agent-01" || ev.Agent != "agent-01" || ev.Title != "Reminders page" {
		t.Errorf("the event doesn't say which agent it is about: %+v", ev)
	}
	if ev.Summary != summary || ev.Cut {
		t.Errorf("summary = %q (cut %v), want the agent's own words whole", ev.Summary, ev.Cut)
	}
	if ev.Changes == nil || *ev.Changes != changes {
		t.Errorf("changes = %+v, want %+v", ev.Changes, changes)
	}
	if ev.PR == nil || ev.PR.Number != 58 {
		t.Errorf("pr = %+v, want #58", ev.PR)
	}
	if !ev.At.Equal(eventTime) {
		t.Errorf("at = %v, want %v", ev.At, eventTime)
	}
	if ev.Question != "" {
		t.Errorf("a finish carries a question ID: %q", ev.Question)
	}
}

// An agent that stopped after a tool call left nothing to quote, and one that
// wrote a report left too much: neither is allowed to become a summary that
// promises what it isn't.
func TestFinishedEventSummaryIsTrimmed(t *testing.T) {
	a := worker("agent-01", "")

	if ev := finishedEvent(a, api.AgentChanges{}, nil, "Done!", eventTime); ev.Summary != "" {
		t.Errorf("an acknowledgement became a summary: %q", ev.Summary)
	}
	if ev := finishedEvent(a, api.AgentChanges{}, nil, "", eventTime); ev.Summary != "" || ev.Cut {
		t.Errorf("nothing at all became a summary: %q", ev.Summary)
	}
	report := longReport()
	ev := finishedEvent(a, api.AgentChanges{}, nil, report, eventTime)
	if !ev.Cut {
		t.Error("an enormous report wasn't marked as cut, so the app shows part of it as all of it")
	}
	if len(ev.Summary) >= len(report) {
		t.Errorf("the summary is %d characters of the agent's %d: the cap isn't doing its job", len(ev.Summary), len(report))
	}
	if !strings.Contains(ev.Summary, "What I did") {
		t.Errorf("the beginning of the summary didn't survive:\n%s", ev.Summary)
	}
}

// The task is the whole of what a new agent's thread has to show, so it is
// kept even when it is a single short line — unlike a finish, where a line
// that short is an acknowledgement rather than a summary.
func TestCreatedEventKeepsAShortTask(t *testing.T) {
	ev := createdEvent(worker("agent-02", "Image bump"), "  Bump the image.  ", eventTime)

	if ev.Kind != api.AgentCreated {
		t.Errorf("kind = %q, want %q", ev.Kind, api.AgentCreated)
	}
	if ev.Summary != "Bump the image." {
		t.Errorf("summary = %q, want the task it was given", ev.Summary)
	}
	if ev.Changes != nil || ev.PR != nil {
		t.Errorf("an agent that has just been made already has changes or a pull request: %+v", ev)
	}
	if ev := createdEvent(worker("agent-03", ""), "", eventTime); ev.Summary != "" {
		t.Errorf("an agent made with no task has a summary: %q", ev.Summary)
	}
}

// A question changes after it is asked, so the event points at it rather than
// copying it: the thread reads it as it is now, escalation and answer included.
func TestQuestionEventsPointAtTheQuestion(t *testing.T) {
	q := state.Question{
		ID: "q1", Project: "hello-stack", Agent: "agent-01",
		Text: "Should the reminders page paginate?", Status: state.QuestionPending, CreatedAt: eventTime,
	}

	asked := questionEvent(q, "Reminders page", api.AgentAsked, q.CreatedAt)
	if asked.Kind != api.AgentAsked || asked.Question != "q1" {
		t.Errorf("asked = %+v, want kind %q about q1", asked, api.AgentAsked)
	}
	if asked.Ref != "hello-stack/agent-01" || asked.Title != "Reminders page" {
		t.Errorf("the event doesn't say which agent asked: %+v", asked)
	}
	if asked.Summary != "" {
		t.Errorf("the question's text was copied into the event: %q", asked.Summary)
	}

	answered := questionEvent(q, "Reminders page", api.AgentAnswered, eventTime.Add(time.Minute))
	if answered.Kind != api.AgentAnswered || answered.Question != "q1" {
		t.Errorf("answered = %+v, want kind %q about q1", answered, api.AgentAnswered)
	}
	if !answered.At.After(asked.At) {
		t.Errorf("the answer is not after the question: %v, %v", answered.At, asked.At)
	}
}

// The lead's prose and the app's thread are two renderings of one event, so
// the notice is built from the event: anything the notice says, the thread has.
func TestFinishNoticeIsWrittenFromTheEvent(t *testing.T) {
	a := worker("agent-01", "Reminders page")
	summary := "Added pagination to the reminders list, twenty a page, with the page in the query string."
	pr := &api.PullRequest{Number: 58, URL: "https://github.com/leciric/agentbox/pull/58"}

	notice := finishNotice(finishedEvent(a, api.AgentChanges{Files: 4, Insertions: 120, Deletions: 8, Dirty: true}, pr, summary, eventTime))
	for _, want := range []string{"agent-01", "Reminders page", "4 file(s), +120/-8", "not yet committed", "PR #58", quote(summary), `read_agent("agent-01")`} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice lost %q:\n%s", want, notice)
		}
	}

	nothing := finishNotice(finishedEvent(worker("agent-02", ""), api.AgentChanges{}, nil, "", eventTime))
	for _, want := range []string{"agent-02 (no title) finished.", "It has changed nothing.", "It left no summary."} {
		if !strings.Contains(nothing, want) {
			t.Errorf("the notice lost %q:\n%s", want, nothing)
		}
	}
	if strings.Contains(nothing, ">") {
		t.Errorf("the notice quotes something that isn't a summary:\n%s", nothing)
	}

	cut := finishNotice(finishedEvent(a, api.AgentChanges{}, nil, longReport(), eventTime))
	if !strings.Contains(cut, "start of a longer summary") {
		t.Errorf("a cut summary isn't reported as cut, so the lead reads part of it as all of it:\n%s", cut)
	}
}

// The events are what the app's threads are made of, so they have to outlive
// the app: a finish, a question and its answer all end up in the project's
// list, newest first, with the question's ID linking the two halves.
func TestAgentEventsArePersistedPerProject(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	saidLast(t, d, a, "Added pagination to the reminders list, twenty a page, with the page in the query string.")
	if err := os.WriteFile(filepath.Join(a.Worktree, "server.mjs"), []byte("// paginated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})
	waitFor(t, "the finish to be recorded", func() bool { return len(eventsOf(t, d, "hello-stack")) > 0 })

	q, err := d.srv.store.Question(ctx, askAndAnswer(t, d, a))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the question and its answer to be recorded", func() bool { return len(eventsOf(t, d, "hello-stack")) >= 3 })

	events := eventsOf(t, d, "hello-stack")
	byKind := map[string]api.AgentEvent{}
	for _, ev := range events {
		byKind[ev.Kind] = ev
	}
	finished, ok := byKind[api.AgentFinished]
	if !ok {
		t.Fatalf("no finish among %d events", len(events))
	}
	if finished.Changes == nil || finished.Changes.Files != 1 || !finished.Changes.Dirty {
		t.Errorf("the finish lost what the agent changed: %+v", finished.Changes)
	}
	if !strings.Contains(finished.Summary, "pagination") {
		t.Errorf("the finish lost the agent's summary: %q", finished.Summary)
	}
	if byKind[api.AgentAsked].Question != q.ID || byKind[api.AgentAnswered].Question != q.ID {
		t.Errorf("the question's two events don't point at question %s: %+v", q.ID, byKind)
	}
	for i := 1; i < len(events); i++ {
		if events[i].At.After(events[i-1].At) {
			t.Errorf("the events aren't newest first: %v then %v", events[i-1].At, events[i].At)
		}
	}
}

// Destroying an agent takes its thread with it, the way it takes its
// conversation: a row in the rail for an agent that is gone has nothing behind it.
func TestAgentEventsGoWithTheAgent(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	kept := addAgent(t, d, repo, "hello-stack", "agent-02", "Something else")
	d.srv.record(ctx, createdEvent(a, "Paginate the reminders", time.Now()))
	d.srv.record(ctx, createdEvent(kept, "Do the other thing", time.Now()))

	if err := d.srv.store.RemoveAgent(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	}

	events := eventsOf(t, d, "hello-stack")
	if len(events) != 1 || events[0].Agent != "agent-02" {
		t.Errorf("after removing agent-01 the project has %d events: %+v", len(events), events)
	}
}

func eventsOf(t *testing.T, d testDaemon, project string) []api.AgentEvent {
	t.Helper()
	out, err := d.client.AgentEvents(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// askAndAnswer runs a question all the way through, the way the in-agent route
// and the lead do, and returns its ID.
func askAndAnswer(t *testing.T, d testDaemon, a state.Agent) string {
	t.Helper()
	ctx := context.Background()
	asked := make(chan string, 1)
	go func() {
		q, err := d.srv.askForTest(ctx, a, "Should the reminders page paginate?", "building the page")
		if err == nil {
			asked <- q.ID
		}
	}()
	var id string
	waitFor(t, "the question to be waiting", func() bool {
		questions, err := d.srv.store.Questions(ctx, a.Project, true)
		if err != nil || len(questions) == 0 {
			return false
		}
		id = questions[0].ID
		return true
	})
	if _, err := d.srv.answerQuestion(ctx, id, "Yes, twenty a page.", "user"); err != nil {
		t.Fatal(err)
	}
	<-asked
	return id
}
