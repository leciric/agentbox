package daemon

import (
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Who may be retired, and why not. These are the rules that decide whether an
// agent's machine is freed, so each one is worth pinning down.
func TestSkipReason(t *testing.T) {
	t.Parallel()
	running := agent.Status{Agent: state.Agent{Name: "agent-01", Branch: "agentbox/agent-01"}, State: "running"}
	stopped := agent.Status{Agent: state.Agent{Name: "agent-01", Branch: "agentbox/agent-01"}, State: "stopped"}
	clean := api.RetireAdvice{Safe: true, Branch: "agentbox/agent-01"}
	dirty := api.RetireAdvice{Safe: false, Reason: "it has uncommitted work: committing it puts it on agentbox/agent-01"}
	long, recent := time.Now().Add(-2*time.Hour), time.Now().Add(-time.Minute)

	for _, tc := range []struct {
		name string
		req  api.RetireRequest
		st   agent.Status
		busy bool
		last *time.Time
		adv  api.RetireAdvice
		idle time.Duration
		want string
	}{
		{name: "an idle agent with committed work is freed", req: api.RetireRequest{How: api.RetireStop}, st: running, adv: clean},
		{name: "a working agent is never swept away", req: api.RetireRequest{How: api.RetireStop}, st: running, busy: true, adv: clean,
			want: "it is still working"},
		{name: "not even with --force, unless it is named", req: api.RetireRequest{How: api.RetireStop, Force: true}, st: running, busy: true, adv: clean,
			want: "it is still working"},
		{name: "naming a working agent and forcing it stops it",
			req: api.RetireRequest{How: api.RetireStop, Force: true, Agents: []string{"agent-01"}}, st: running, busy: true, adv: clean},
		{name: "uncommitted work is protected", req: api.RetireRequest{How: api.RetireDestroy}, st: running, adv: dirty, want: dirty.Reason},
		{name: "naming it is not enough for uncommitted work",
			req: api.RetireRequest{How: api.RetireDestroy, Agents: []string{"agent-01"}}, st: running, adv: dirty, want: dirty.Reason},
		{name: "--force gets past uncommitted work",
			req: api.RetireRequest{How: api.RetireDestroy, Force: true, Agents: []string{"agent-01"}}, st: running, adv: dirty},
		{name: "stopping an already stopped machine does nothing", req: api.RetireRequest{How: api.RetireStop}, st: stopped, adv: clean,
			want: "its machine is already stopped"},
		{name: "but destroying one still frees its worktree", req: api.RetireRequest{How: api.RetireDestroy}, st: stopped, adv: clean},
		{name: "recently active agents are left alone when idle-for is set",
			req: api.RetireRequest{How: api.RetireStop, IdleFor: "30m"}, st: running, adv: clean, last: &recent, idle: 30 * time.Minute,
			want: "it was active less than 30m ago"},
		{name: "long-idle ones are freed",
			req: api.RetireRequest{How: api.RetireStop, IdleFor: "30m"}, st: running, adv: clean, last: &long, idle: 30 * time.Minute},
		{name: "an agent that has never been used has no idle time to measure",
			req: api.RetireRequest{How: api.RetireStop, IdleFor: "30m"}, st: running, adv: clean, idle: 30 * time.Minute,
			want: "it was active less than 30m ago"},
		{name: "naming it skips the idle-for wait",
			req: api.RetireRequest{How: api.RetireStop, IdleFor: "30m", Agents: []string{"agent-01"}}, st: running, adv: clean, last: &recent, idle: 30 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipReason(tc.req, tc.st, tc.busy, tc.last, tc.adv, tc.idle); got != tc.want {
				t.Errorf("skipReason() = %q, want %q", got, tc.want)
			}
		})
	}
}
