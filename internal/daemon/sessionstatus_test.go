package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// What waits on the user comes first, then a chat that stopped or stalled,
// then a turn; a lead's model deciding about a question isn't the user's.
func TestSessionStanding(t *testing.T) {
	for _, tc := range []struct {
		name      string
		att       chat.Attention
		questions int
		status    string
		needs     int
	}{
		{"never started", chat.Attention{}, 0, api.SessionIdle, 0},
		{"ready", chat.Attention{State: api.ChatReady}, 0, api.SessionIdle, 0},
		{"off", chat.Attention{State: api.ChatOff}, 0, api.SessionIdle, 0},
		{"in a turn", chat.Attention{State: api.ChatRunning}, 0, api.SessionWorking, 0},
		{"starting", chat.Attention{State: api.ChatStarting}, 0, api.SessionWorking, 0},
		{"stopped on an error", chat.Attention{State: api.ChatError}, 0, api.SessionFailed, 0},
		{"stalled", chat.Attention{State: api.ChatRunning, Stalled: true}, 0, api.SessionFailed, 0},
		{"its own permission", chat.Attention{State: api.ChatWaiting, Permissions: 1}, 0, api.SessionNeedsYou, 1},
		{"an agent's permission while it works", chat.Attention{State: api.ChatRunning, AgentPermissions: 2}, 0, api.SessionNeedsYou, 2},
		{"a question while idle", chat.Attention{State: api.ChatReady}, 1, api.SessionNeedsYou, 1},
		{"needs you over failed", chat.Attention{State: api.ChatError, AgentPermissions: 1}, 2, api.SessionNeedsYou, 3},
	} {
		status, needs := sessionStanding(tc.att, tc.questions)
		if status != tc.status || needs != tc.needs {
			t.Errorf("%s: sessionStanding() = %s, %d; want %s, %d", tc.name, status, needs, tc.status, tc.needs)
		}
	}
}

// A question waits in its agent's session; one from an agent no open session
// owns waits in the open session most recently active, as its notices go;
// one the lead still decides about, or from an agent that's gone, waits
// nowhere.
func TestQuestionSessions(t *testing.T) {
	now := time.Now()
	open := []state.LeadSession{{ID: "main"}, {ID: "prod"}}
	activity := map[string]time.Time{"main": now.Add(-time.Hour), "prod": now}
	agents := []state.Agent{
		{Name: "agent-01", CreatedBySession: "main"},
		{Name: "agent-02", CreatedBySession: "closed-one"},
		{Name: "agent-03"},
		{Name: "agent-04", CreatedBySession: "main"},
	}
	q := func(agent, status string) state.Question { return state.Question{Agent: agent, Status: status} }
	got := questionSessions(open, activity, agents, []state.Question{
		q("agent-01", state.QuestionEscalated),
		q("agent-01", state.QuestionEscalated),
		q("agent-02", state.QuestionEscalated),
		q("agent-03", state.QuestionEscalated),
		q("agent-04", state.QuestionPending),
		q("agent-99", state.QuestionEscalated),
	})
	if len(got) != 2 || got["main"] != 2 || got["prod"] != 2 {
		t.Errorf("questionSessions() = %v, want main 2 and prod 2", got)
	}
	if got := questionSessions(nil, nil, agents, []state.Question{q("agent-01", state.QuestionEscalated)}); len(got) != 0 {
		t.Errorf("with no open session = %v, want none", got)
	}
}

func TestUnread(t *testing.T) {
	created := time.Now().Add(-time.Hour)
	at := func(d time.Duration) *time.Time { t := created.Add(d); return &t }
	line := func(d time.Duration) *api.ChatLine { return &api.ChatLine{At: *at(d)} }
	for _, tc := range []struct {
		name string
		last *api.ChatLine
		seen *time.Time
		want bool
	}{
		{"nothing said", nil, nil, false},
		{"never seen, said since it opened", line(time.Minute), nil, true},
		{"seen after the last message", line(time.Minute), at(2 * time.Minute), false},
		{"a message after it was seen", line(3 * time.Minute), at(2 * time.Minute), true},
	} {
		if got := unread(tc.last, tc.seen, created); got != tc.want {
			t.Errorf("%s: unread() = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// The list says where each session stands, a question passed on to the user
// is pushed as soon as it lands, and a session seen in one window is read in
// every other.
func TestSessionStatusIsListedAndPushed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	sessions, err := d.client.LeadSessions(ctx, "hello-stack")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("LeadSessions() = %+v, %v", sessions, err)
	}
	if s := sessions[0]; s.Status != api.SessionIdle || s.NeedsYou != 0 || s.Unread || s.SeenAt != nil {
		t.Fatalf("a new project's session = %+v, want idle, read, never seen", s)
	}
	addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	events, stop := d.srv.events.subscribe()
	defer stop()
	q := state.Question{ID: "5e5510aa", Project: "hello-stack", Agent: "agent-01", Text: "Paginate?",
		Status: state.QuestionEscalated, CreatedAt: time.Now()}
	if err := d.srv.store.AddQuestion(ctx, q); err != nil {
		t.Fatal(err)
	}
	d.srv.events.publish(api.EventQuestion, toAPIQuestion(q))
	change := nextStatus(t, events)
	if len(change.Sessions) != 1 || change.Sessions[0].Status != api.SessionNeedsYou || change.Sessions[0].NeedsYou != 1 {
		t.Fatalf("pushed %+v, want the one session needing you once", change)
	}

	seen, err := d.client.SeeLeadSession(ctx, "hello-stack", sessions[0].ID)
	if err != nil || seen.SeenAt == nil {
		t.Fatalf("SeeLeadSession() = %+v, %v; want it seen", seen, err)
	}
	if change := nextStatus(t, events); change.Sessions[0].SeenAt == nil {
		t.Fatalf("pushed %+v after it was seen, want its seenAt", change)
	}

	if _, err := d.srv.answerQuestion(ctx, q.ID, "Twenty a page.", "user"); err != nil {
		t.Fatal(err)
	}
	if change := nextStatus(t, events); change.Sessions[0].Status != api.SessionIdle {
		t.Fatalf("pushed %+v once answered, want it idle again", change)
	}
}

func nextStatus(t *testing.T, events <-chan api.Event) api.LeadSessionStatusChange {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != api.EventLeadSessionStatus {
				continue
			}
			var change api.LeadSessionStatusChange
			if err := json.Unmarshal(ev.Data, &change); err != nil {
				t.Fatal(err)
			}
			return change
		case <-deadline:
			t.Fatal("no session status was pushed")
		}
	}
}
