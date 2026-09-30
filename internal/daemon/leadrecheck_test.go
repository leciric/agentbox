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
		{"no agents, no queue", recheckInput{Slots: 2, Free: 2}},
		{"only working agents", recheckInput{Slots: 2, Agents: []recheckAgent{{Name: "a1", Busy: true}, {Name: "a2", Busy: true}}}},
		{"idle, but not for long", recheckInput{Slots: 2, Free: 1, Agents: []recheckAgent{{Name: "a1", IdleFor: 5 * time.Minute}}}},
	} {
		if note, _, wake := recheckNote(c.in, idleAfter); wake {
			t.Errorf("%s: woke the lead with %q", c.name, note)
		}
	}
}

func TestRecheckNoteWakes(t *testing.T) {
	t.Parallel()
	idleAfter := 20 * time.Minute
	in := recheckInput{Queued: 2, Slots: 3, Free: 0, Agents: []recheckAgent{
		{Name: "agent-03", IdleFor: 42 * time.Minute, Finished: true, Report: `done "CSV export works"`, PR: "#17 checks passing",
			Memory: 1 << 30, MemoryPeak: 3 << 30, CPU: 2, CPUPeak: 180},
		{Name: "agent-04", Busy: true},
		{Name: "agent-05", IdleFor: time.Hour, Dirty: true},
	}}
	note, key, wake := recheckNote(in, idleAfter)
	if !wake {
		t.Fatal("didn't wake the lead with two queued and an agent idle 42m")
	}
	for _, want := range []string{
		"2 waiting, 0 of 3 slots free", "agent-03 finished, idle 42m", `report: done "CSV export works"`,
		"PR #17 checks passing", "mem 1.0G/3.0G peak, cpu 2%/180%", "agent-04 working", "agent-05 idle 1h; UNCOMMITTED work",
		"never one with uncommitted or unpushed work",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	// Small: a line an agent, and one each for the queue and the rule.
	if lines := strings.Count(note, "\n") + 1; lines != 5 {
		t.Errorf("note is %d lines:\n%s", lines, note)
	}
	// The same state, only later, has the same key; a change doesn't.
	in.Agents[0].IdleFor = 2 * time.Hour
	if _, later, _ := recheckNote(in, idleAfter); later != key {
		t.Errorf("key changed with the idle time alone: %q, %q", key, later)
	}
	in.Queued = 1
	if _, changed, _ := recheckNote(in, idleAfter); changed == key {
		t.Error("key didn't change with the queue")
	}
	// A queue alone is reason enough.
	if _, _, wake := recheckNote(recheckInput{Queued: 1, Slots: 1}, idleAfter); !wake {
		t.Error("a waiting queue didn't wake the lead")
	}
}

// The recheck is off until turned on, only wakes a project's chat with
// something to act on, never twice for the same state, and no more often than
// its interval.
func TestRecheckLeads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var mu sync.Mutex
	var told []string
	q := newQueueTest(t, 16*gib, map[string]int64{"p": 2 * gib, "quiet": 2 * gib}, "[]")
	q.srv.recheckTell = func(_ context.Context, project, note string) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, project+": "+note)
	}
	tells := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), told...)
	}
	for _, p := range []string{"p", "quiet"} {
		q.addProject(t, p)
		lead := state.Agent{Project: p, Name: state.LeadName, Role: state.RoleLead, Status: state.AgentReady, Worktree: t.TempDir(), CreatedAt: time.Now()}
		// The daemon may have made it already.
		if err := q.srv.store.AddAgent(ctx, lead); err != nil && !errors.Is(err, state.ErrExists) {
			t.Fatal(err)
		}
	}
	if err := q.srv.store.SetProjectSlots(ctx, "p", 1); err != nil {
		t.Fatal(err)
	}
	q.enqueue(t, "p", "q1")
	q.enqueue(t, "p", "q2")
	q.srv.admitQueued(ctx) // q1 starts, holding the one slot; q2 waits

	now := time.Now()
	q.srv.recheckLeads(ctx, now)
	if got := tells(); len(got) != 0 {
		t.Fatalf("recheck off, told %v", got)
	}
	settings, err := q.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadRecheck: ptr(true), LeadRecheckMinutes: ptr(20)})
	if err != nil || !settings.LeadRecheck || settings.LeadRecheckMinutes != 20 {
		t.Fatalf("turning it on = %+v, %v", settings, err)
	}
	if _, err := q.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadRecheckMinutes: ptr(1)}); err == nil {
		t.Error("a recheck every minute was taken")
	}

	q.srv.recheckLeads(ctx, now)
	got := tells()
	if len(got) != 1 || !strings.HasPrefix(got[0], "p: [recheck] Queue: 1 waiting, 0 of 1 slots free.") {
		t.Fatalf("told %q, want only p, about its queue", got)
	}
	// Not due yet.
	q.srv.recheckLeads(ctx, now.Add(5*time.Minute))
	// Due, but nothing changed: skipped.
	q.srv.recheckLeads(ctx, now.Add(21*time.Minute))
	if got := tells(); len(got) != 1 {
		t.Fatalf("told again with nothing changed: %q", got)
	}
	// Something changed: told.
	q.enqueue(t, "p", "q3")
	q.srv.recheckLeads(ctx, now.Add(42*time.Minute))
	if got := tells(); len(got) != 2 || !strings.Contains(got[1], "2 waiting") {
		t.Fatalf("after another was queued, told %q", got)
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
	if note, _, wake := recheckNote(recheckInput{Slots: 2, Agents: stuck}, idleAfter); wake {
		t.Errorf("woke the chat again for what it was told of already: %q", note)
	}
	note, _, wake := recheckNote(recheckInput{Queued: 1, Slots: 2, Agents: stuck}, idleAfter)
	if !wake {
		t.Fatal("a queue didn't wake the chat")
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
