package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

func TestRecheckNoteSkipsWhenNothingToDo(t *testing.T) {
	t.Parallel()
	idleAfter := 20 * time.Minute
	for _, c := range []struct {
		name string
		in   recheckInput
	}{
		{"no agents", recheckInput{}},
		{"only working agents", recheckInput{Agents: []recheckAgent{{Name: "a1", Busy: true}, {Name: "a2", Busy: true}}}},
		{"idle, but not for long", recheckInput{Agents: []recheckAgent{{Name: "a1", IdleFor: 5 * time.Minute}}}},
	} {
		if note, _, wake := recheckNote(c.in, idleAfter); wake {
			t.Errorf("%s: woke the lead with %q", c.name, note)
		}
	}
}

func TestRecheckNoteWakes(t *testing.T) {
	t.Parallel()
	idleAfter := 20 * time.Minute
	in := recheckInput{Agents: []recheckAgent{
		{Name: "agent-03", IdleFor: 42 * time.Minute, Finished: true, Report: `done "CSV export works"`, PR: "#17 checks passing",
			Memory: 1 << 30, CPU: 2},
		{Name: "agent-04", Busy: true},
		{Name: "agent-05", IdleFor: time.Hour, Dirty: true},
	}}
	note, key, wake := recheckNote(in, idleAfter)
	if !wake {
		t.Fatal("didn't wake the lead with an agent idle 42m")
	}
	for _, want := range []string{
		"agent-03 finished, idle 42m", `report: done "CSV export works"`,
		"PR #17 checks passing", "mem 1.0G, cpu 2%", "agent-04 working", "agent-05 idle 1h; UNCOMMITTED work",
		"never one with uncommitted or unpushed work",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "Queue") || strings.Contains(note, "slots") {
		t.Errorf("note speaks of a queue:\n%s", note)
	}
	// Small: a header, a line an agent, and one for the rule.
	if lines := strings.Count(note, "\n") + 1; lines != 5 {
		t.Errorf("note is %d lines:\n%s", lines, note)
	}
	// The same state, only later, has the same key; a change doesn't.
	in.Agents[0].IdleFor = 2 * time.Hour
	if _, later, _ := recheckNote(in, idleAfter); later != key {
		t.Errorf("key changed with the idle time alone: %q, %q", key, later)
	}
	in.Agents[2].Unpushed = true
	if _, changed, _ := recheckNote(in, idleAfter); changed == key {
		t.Error("key didn't change with an agent's unpushed work")
	}
}

// The recheck is off until turned on, only wakes a project's chat with
// something to act on, never twice for the same state, and no more often than
// its interval.
func TestRecheckLeads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	var mu sync.Mutex
	var told []string
	// "p" has an agent idle for three hours; "quiet" has none.
	d, a := newAutoStopIdleTest(t, "Running", now.Add(-3*time.Hour))
	srv := d.srv
	srv.recheckTell = func(_ context.Context, project, note string) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, project+": "+note)
	}
	tells := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), told...)
	}
	if err := srv.store.AddProject(ctx, state.Project{Name: "quiet", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"p", "quiet"} {
		lead := state.Agent{Project: p, Name: state.LeadName, Role: state.RoleLead, Status: state.AgentReady, Worktree: t.TempDir(), CreatedAt: time.Now()}
		// The daemon may have made it already.
		if err := srv.store.AddAgent(ctx, lead); err != nil && !errors.Is(err, state.ErrExists) {
			t.Fatal(err)
		}
	}

	srv.recheckLeads(ctx, now)
	if got := tells(); len(got) != 0 {
		t.Fatalf("recheck off, told %v", got)
	}
	settings, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadRecheck: ptr(true), LeadRecheckMinutes: ptr(20)})
	if err != nil || !settings.LeadRecheck || settings.LeadRecheckMinutes != 20 {
		t.Fatalf("turning it on = %+v, %v", settings, err)
	}
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadRecheckMinutes: ptr(1)}); err == nil {
		t.Error("a recheck every minute was taken")
	}

	srv.recheckLeads(ctx, now)
	got := tells()
	if len(got) != 1 || !strings.HasPrefix(got[0], "p: [recheck]") || !strings.Contains(got[0], "a1 idle 3h") {
		t.Fatalf("told %q, want only p, about its idle agent", got)
	}
	// Not due yet.
	srv.recheckLeads(ctx, now.Add(5*time.Minute))
	// Due, but nothing changed: skipped.
	srv.recheckLeads(ctx, now.Add(21*time.Minute))
	if got := tells(); len(got) != 1 {
		t.Fatalf("told again with nothing changed: %q", got)
	}
	// Something changed: told.
	if err := srv.store.SavePRWatch(ctx, state.PRWatch{Project: a.Project, Number: 7, Agent: a.Name, Checks: "passing", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	srv.recheckLeads(ctx, now.Add(42*time.Minute))
	if got := tells(); len(got) != 2 || !strings.Contains(got[1], "PR #7 checks passing") {
		t.Fatalf("after its pull request opened, told %q", got)
	}
}

// A stalled agent reads as stalled, not working, and one whose AI tool exited
// as stopped, not idle; neither wakes the chat on its own, which was told when
// it happened (stallwatch.go).
func TestRecheckNoteSaysStalledAndStopped(t *testing.T) {
	t.Parallel()
	idleAfter := 20 * time.Minute
	stuck := []recheckAgent{
		{Name: "agent-07", Busy: true, Stalled: 22 * time.Minute},
		{Name: "agent-12", IdleFor: 5 * time.Minute, ChatStopped: "Claude Code exited (exit status 143): ...killed."},
	}
	if note, _, wake := recheckNote(recheckInput{Agents: stuck}, idleAfter); wake {
		t.Errorf("woke the chat again for what it was told of already: %q", note)
	}
	// An agent idle for a whole recheck does wake it, and the stuck ones are
	// then told apart in the note.
	idle := append([]recheckAgent{{Name: "agent-09", IdleFor: time.Hour}}, stuck...)
	note, _, wake := recheckNote(recheckInput{Agents: idle}, idleAfter)
	if !wake {
		t.Fatal("an agent idle for an hour didn't wake the chat")
	}
	for _, want := range []string{
		"agent-07 STALLED, no progress for 22m",
		"agent-12 CHAT STOPPED (Claude Code exited (exit status 143): ...killed.), idle 5m",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "agent-07 working") {
		t.Errorf("a stalled agent reads as working:\n%s", note)
	}
}
