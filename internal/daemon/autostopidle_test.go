package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// autoStopIdleInstances is one agent, at the given Incus status.
func autoStopIdleInstances(instance, status string) string {
	return fmt.Sprintf(`[{"name":%q,"status":%q,"config":{},"expanded_config":{},`+
		`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]`, instance, status)
}

// newAutoStopIdleTest starts a daemon with one project and one agent, and
// returns it with the store's clock still real: every test drives
// stopIdleIn/stopIdleAgents with a `now` of its own choosing instead, so
// nothing here has to wait out an idle time for real.
func newAutoStopIdleTest(t *testing.T, status string, createdAt time.Time) (testDaemon, state.Agent) {
	t.Helper()
	instance := "ab-p-a1"
	d := startTestDaemon(t, t.TempDir(), cpuBudgetIncus, testConfig{instances: autoStopIdleInstances(instance, status)})
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "p", Name: "a1", Instance: instance, AI: "none",
		Branch: "agentbox/a1", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: createdAt,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	return d, a
}

func setAutoStopIdle(t *testing.T, d testDaemon, on bool, idleTime time.Duration) {
	t.Helper()
	ctx := context.Background()
	if err := d.srv.store.SetFlag(ctx, state.SettingAutoStopIdle, on); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetSetting(ctx, state.SettingIdleTime, fmt.Sprint(int(idleTime/time.Second))); err != nil {
		t.Fatal(err)
	}
}

func instanceStatus(t *testing.T, d testDaemon) string {
	t.Helper()
	insts, err := d.srv.manager(nil).Incus.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range insts {
		return inst.Status
	}
	t.Fatal("no instance")
	return ""
}

// TestStopIdleAgentsOff checks that a long-idle agent is left alone while
// "auto-stop idle agents" is off: an installation that never turned it on
// keeps exactly the behaviour it always had.
func TestStopIdleAgentsOff(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d, _ := newAutoStopIdleTest(t, "Running", now.Add(-3*time.Hour))
	setAutoStopIdle(t, d, false, 2*time.Hour)

	d.srv.stopIdleAgents(context.Background(), now)

	if got := instanceStatus(t, d); got != "Running" {
		t.Errorf("instance = %q, want still Running", got)
	}
}

// TestStopIdleAgentsRunning checks the central rule: a running agent with no
// activity at all is stopped once its idle time has passed, and not before.
func TestStopIdleAgentsRunning(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d, a := newAutoStopIdleTest(t, "Running", now.Add(-3*time.Hour))
	setAutoStopIdle(t, d, true, 2*time.Hour)

	// Idle for 3h, but the idle time hasn't passed yet as of 1h in.
	d.srv.stopIdleAgents(context.Background(), now.Add(-2*time.Hour))
	if got := instanceStatus(t, d); got != "Running" {
		t.Errorf("before its idle time passed: instance = %q, want still Running", got)
	}

	d.srv.stopIdleAgents(context.Background(), now)
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("after its idle time passed: instance = %q, want Stopped", got)
	}

	events, err := d.srv.store.AgentEvents(context.Background(), a.Project)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range events {
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) != nil {
			continue
		}
		if ev.Kind == api.AgentIdleStopped && ev.Ref == a.Ref() {
			found = true
			if ev.Summary != "Stopped after 2h idle" {
				t.Errorf("summary = %q, want %q", ev.Summary, "Stopped after 2h idle")
			}
		}
	}
	if !found {
		t.Error("no idle_stopped event was recorded")
	}
}

// TestStopIdleAgentsRecentChat checks that a chat turn, however old the agent
// itself, resets the idle clock: LastChatItem is the one thing removeReason's
// idleOf already tracks, so "auto-stop idle agents" has to read it the same
// way.
func TestStopIdleAgentsRecentChat(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d, a := newAutoStopIdleTest(t, "Running", now.Add(-3*time.Hour))
	setAutoStopIdle(t, d, true, 2*time.Hour)

	data, err := json.Marshal(struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}{UpdatedAt: now.Add(-30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SaveChatItems(context.Background(), a.Project, a.Name, []state.ChatItem{
		{ID: "item-1", Position: 1, Data: data},
	}); err != nil {
		t.Fatal(err)
	}

	d.srv.stopIdleAgents(context.Background(), now)

	if got := instanceStatus(t, d); got != "Running" {
		t.Errorf("chat active 30m ago: instance = %q, want still Running", got)
	}
}

// TestStopIdleAgentsWaitingQuestion checks that a question or credential
// request nobody has answered yet keeps an agent from being stopped, however
// long it has otherwise gone untouched: stopping it would strand whoever it
// is waiting on.
func TestStopIdleAgentsWaitingQuestion(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d, a := newAutoStopIdleTest(t, "Running", now.Add(-3*time.Hour))
	setAutoStopIdle(t, d, true, 2*time.Hour)

	if err := d.srv.store.AddQuestion(context.Background(), state.Question{
		ID: "q1", Project: a.Project, Agent: a.Name, Kind: state.QuestionDecision,
		Text: "which way?", Status: state.QuestionPending, CreatedAt: now.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	d.srv.stopIdleAgents(context.Background(), now)

	if got := instanceStatus(t, d); got != "Running" {
		t.Errorf("a question is waiting: instance = %q, want still Running", got)
	}
}

// TestStopIdleAgentsPaused checks that a paused agent is counted idle from
// when it was paused, not from anything before that: it holds its RAM and
// swap whether or not somebody was still talking to it right up to the
// moment it was frozen.
func TestStopIdleAgentsPaused(t *testing.T) {
	t.Parallel()
	now := time.Now()
	// Created long ago, but paused only recently: idle should count from the
	// pause, so it isn't stopped yet.
	d, a := newAutoStopIdleTest(t, "Frozen", now.Add(-30*24*time.Hour))
	setAutoStopIdle(t, d, true, 2*time.Hour)
	if err := d.srv.store.SetPausedAt(context.Background(), a.Project, a.Name, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	d.srv.stopIdleAgents(context.Background(), now)
	if got := instanceStatus(t, d); got != "Frozen" {
		t.Errorf("paused 30m ago: instance = %q, want still Frozen", got)
	}

	d.srv.stopIdleAgents(context.Background(), now.Add(2*time.Hour))
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("paused more than its idle time ago: instance = %q, want Stopped", got)
	}
}
