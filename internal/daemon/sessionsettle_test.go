package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The settle rule: a session settles only once it has a pull request, every
// one merged or closed, nothing of it busy, its pull requests read, and idle
// for the project's time.
func TestTheSettleRule(t *testing.T) {
	t.Parallel()
	now := time.Now()
	day := 24 * time.Hour
	idle := now.Add(-25 * time.Hour)
	for _, c := range []struct {
		name  string
		facts settleFacts
		after time.Duration
		want  bool
	}{
		{"merged and idle a day", settleFacts{IdleSince: idle, Known: true, PRs: []string{"merged"}}, day, true},
		{"merged and closed", settleFacts{IdleSince: idle, Known: true, PRs: []string{"merged", "closed"}}, day, true},
		{"one still open", settleFacts{IdleSince: idle, Known: true, PRs: []string{"merged", "open"}}, day, false},
		{"no pull request at all", settleFacts{IdleSince: idle, Known: true}, day, false},
		{"an agent working", settleFacts{IdleSince: idle, Known: true, PRs: []string{"merged"}, Working: true}, day, false},
		{"its lead busy", settleFacts{IdleSince: idle, Known: true, PRs: []string{"merged"}, LeadBusy: true}, day, false},
		{"pull requests not read yet", settleFacts{IdleSince: idle, PRs: []string{"merged"}}, day, false},
		{"idle less than the time", settleFacts{IdleSince: now.Add(-23 * time.Hour), Known: true, PRs: []string{"merged"}}, day, false},
		{"a shorter time", settleFacts{IdleSince: now.Add(-2 * time.Hour), Known: true, PRs: []string{"merged"}}, time.Hour, true},
		{"settling off", settleFacts{IdleSince: idle, Known: true, PRs: []string{"merged"}}, 0, false},
	} {
		if got := c.facts.settles(c.after, now); got != c.want {
			t.Errorf("%s: settles = %v, want %v", c.name, got, c.want)
		}
	}
	// A pull request merged an hour ago counts as the session's last
	// activity: a day from then.
	var f settleFacts
	f.IdleSince = idle
	merged := now.Add(-time.Hour)
	f.add(api.SessionOutput{Agents: []api.SessionAgent{{PR: &api.PullRequest{State: "merged", UpdatedAt: &merged}}}}, true)
	if f.settles(day, now) || !f.IdleSince.Equal(merged) {
		t.Errorf("after a merge an hour ago: %+v settles", f)
	}
}

// A session whose agents' work is all in settles into the history, marked
// so, with the app told; one with an open pull request, or in a project with
// settling off, stays. An agent retired with its pull request still counts.
// Reopening it (the app's Undo) gives it its idle time again.
func TestASessionSettlesOnceItsWorkIsIn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	ls, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{Topic: "Fix login"})
	if err != nil {
		t.Fatal(err)
	}
	a1 := addAgent(t, d, repo, "hello-stack", "agent-01", "Fix the cookie")
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", a1.Name, ls.ID); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	prState := "open"
	d.srv.sessionPRs = func(p state.Project, agents []state.Agent) (map[string]*api.PullRequest, bool) {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]*api.PullRequest{}
		for _, a := range agents {
			if a.Name == a1.Name {
				out[a.Name] = &api.PullRequest{Number: 12, State: prState, Checks: "passing"}
			}
		}
		return out, true
	}
	events, stop := d.srv.events.subscribe()
	defer stop()
	p, err := d.srv.store.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(25 * time.Hour)

	out, err := d.client.SessionOutput(ctx, "hello-stack", ls.ID)
	if err != nil || len(out.Agents) != 1 || out.Agents[0].PR == nil || out.Agents[0].PR.Number != 12 || out.Agents[0].Archived {
		t.Fatalf("SessionOutput() = %+v, %v", out, err)
	}
	if got := d.srv.settleIn(ctx, p, later); len(got) != 0 {
		t.Fatalf("settled %v with its pull request open", got)
	}

	mu.Lock()
	prState = "merged"
	mu.Unlock()
	if got := d.srv.settleIn(ctx, p, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("settled %v after an hour", got)
	}
	if err := d.srv.store.SetProjectSettleHours(ctx, "hello-stack", 0); err != nil {
		t.Fatal(err)
	}
	off, _ := d.srv.store.Project(ctx, "hello-stack")
	if got := d.srv.settleIn(ctx, off, later); len(got) != 0 {
		t.Fatalf("settled %v with settling off", got)
	}

	// Retired with its pull request merged: still the session's.
	a, err := d.srv.store.Agent(ctx, "hello-stack", a1.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.ArchiveAgent(ctx, a, "", state.ArchivedPR{Number: 12, State: state.PRMerged}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out, _ := d.client.SessionOutput(ctx, "hello-stack", ls.ID); len(out.Agents) != 1 || !out.Agents[0].Archived || out.Agents[0].PR == nil {
		t.Fatalf("after retiring its agent, SessionOutput() = %+v", out)
	}

	d.srv.settleSessions(ctx, later) // still off: nothing
	if err := d.srv.store.SetProjectSettleHours(ctx, "hello-stack", 24); err != nil {
		t.Fatal(err)
	}
	d.srv.settleSessions(ctx, later)
	history, err := d.client.ClosedLeadSessions(ctx, "hello-stack")
	if err != nil || len(history) != 1 || history[0].ID != ls.ID || !history[0].Settled {
		t.Fatalf("the history = %+v, %v; want the session, settled", history, err)
	}
	if !sawSettled(t, events, ls.ID) {
		t.Error("the app wasn't told the session settled")
	}

	// Undo.
	if _, err := d.client.ReopenLeadSession(ctx, "hello-stack", ls.ID); err != nil {
		t.Fatal(err)
	}
	if got := d.srv.settleIn(ctx, p, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Errorf("settled again an hour after Undo: %v", got)
	}
	open, _ := d.client.LeadSessions(ctx, "hello-stack")
	for _, s := range open {
		if s.ID == ls.ID && s.Settled {
			t.Error("reopened, it still reads settled")
		}
	}
	if got := d.srv.settleIn(ctx, p, time.Now().Add(25*time.Hour)); len(got) != 1 {
		t.Errorf("a day after Undo, settled %v", got)
	}
}

func sawSettled(t *testing.T, events <-chan api.Event, id string) bool {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Type != api.EventSessionsSettled {
				continue
			}
			var got api.SessionsSettled
			if err := json.Unmarshal(e.Data, &got); err != nil {
				t.Fatal(err)
			}
			for _, s := range got.Sessions {
				if s.ID == id && s.Settled {
					return true
				}
			}
		case <-deadline:
			return false
		}
	}
}

// A snoozed session is open but listed with its wake time; it comes back at
// that time, or sooner when one of its agents asks the chat something or
// needs a permission. A time already passed is refused.
func TestASnoozedSessionWakesAtItsTimeOrWhenItsAgentNeedsYou(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	ls, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{Topic: "Fix login"})
	if err != nil {
		t.Fatal(err)
	}
	a1 := addAgent(t, d, repo, "hello-stack", "agent-01", "Fix the cookie")
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", a1.Name, ls.ID); err != nil {
		t.Fatal(err)
	}
	a1, _ = d.srv.store.Agent(ctx, "hello-stack", a1.Name)
	snoozed := func() *time.Time {
		t.Helper()
		open, err := d.client.LeadSessions(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range open {
			if s.ID == ls.ID {
				return s.SnoozedUntil
			}
		}
		t.Fatalf("session %s isn't open: %+v", ls.ID, open)
		return nil
	}
	if _, err := d.client.SnoozeLeadSession(ctx, "hello-stack", ls.ID, time.Now().Add(-time.Minute)); err == nil {
		t.Error("a snooze until the past was taken")
	}
	until := time.Now().Add(3 * time.Hour)
	got, err := d.client.SnoozeLeadSession(ctx, "hello-stack", ls.ID, until)
	if err != nil || got.SnoozedUntil == nil || !got.SnoozedUntil.Equal(until.Truncate(time.Millisecond)) {
		t.Fatalf("SnoozeLeadSession() = %+v, %v", got, err)
	}

	// At its time.
	d.srv.wakeSnoozed(ctx, time.Now().Add(time.Hour))
	if snoozed() == nil {
		t.Fatal("woke two hours early")
	}
	d.srv.wakeSnoozed(ctx, until.Add(time.Second))
	if at := snoozed(); at != nil {
		t.Fatalf("still snoozed after its time: %v", at)
	}

	// Its agent asks something.
	if _, err := d.client.SnoozeLeadSession(ctx, "hello-stack", ls.ID, until); err != nil {
		t.Fatal(err)
	}
	askCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = d.srv.askForTest(askCtx, a1, "Which cookie?", "fixing login")
	}()
	waitFor(t, "the session to wake for its agent's question", func() bool { return snoozed() == nil })
	cancel()
	<-done

	// Its agent needs a permission; one answered wakes nothing.
	if _, err := d.client.SnoozeLeadSession(ctx, "hello-stack", ls.ID, until); err != nil {
		t.Fatal(err)
	}
	answered := api.ChatAgentPermission{Ref: a1.Ref(), Item: "p0", Permission: api.ChatPermission{Title: "Run go test", Outcome: "allowed"}}
	d.srv.agentPermission(a1, answered)
	time.Sleep(100 * time.Millisecond)
	if snoozed() == nil {
		t.Fatal("an answered permission woke the session")
	}
	d.srv.agentPermission(a1, api.ChatAgentPermission{Ref: a1.Ref(), Item: "p1", Permission: api.ChatPermission{Title: "Run go test"}})
	waitFor(t, "the session to wake for its agent's permission", func() bool { return snoozed() == nil })

	// An agent of no session wakes nobody.
	if _, err := d.client.SnoozeLeadSession(ctx, "hello-stack", ls.ID, until); err != nil {
		t.Fatal(err)
	}
	d.srv.wakeSessionOf(ctx, state.Agent{Project: "hello-stack", Name: "agent-99"}, "asks something")
	if snoozed() == nil {
		t.Error("an agent of no session woke it")
	}
}
