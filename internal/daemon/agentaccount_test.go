package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// TestSwitchAgentAccount is switch_agent_account and the agent's composer:
// refused mid-turn, then the agent's chat restarts on the new account and
// resumes its session, and its composer lists the accounts it can move to.
func TestSwitchAgentAccount(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), readyOneAgentIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	addProjectAllowingEvery(t, d, d.fixtureRepo(t, "hello-stack"))
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "claude", ClaudeAccount: "default",
		Branch: "agentbox/agent-01", Worktree: "/worktrees/agent-01", Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	tool := &resumingTool{hold: make(chan struct{})}
	d.srv.chat.Launch, d.srv.chat.Prepare = tool.launch, nil
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))

	if _, err := d.srv.chat.Send(a, "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn to start", func() bool { return d.srv.chat.Busy(a) })
	if _, err := lead.SwitchAgentAccount(ctx, a.Name, api.AgentAccountRequest{Account: "work"}); err == nil || !strings.Contains(err.Error(), "in the middle of a turn") {
		t.Errorf("switching mid-turn = %v, want it refused", err)
	}
	close(tool.hold)
	waitFor(t, "the turn to end", func() bool { return !d.srv.chat.Busy(a) })

	if _, err := lead.SwitchAgentAccount(ctx, a.Name, api.AgentAccountRequest{Account: "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("an unknown account = %v, want it refused", err)
	}
	if _, err := lead.SwitchAgentAccount(ctx, a.Name, api.AgentAccountRequest{OpenCodeAccounts: map[string]string{"anthropic": "work"}}); err == nil || !strings.Contains(err.Error(), "pass account") {
		t.Errorf("OpenCode accounts for a Claude Code agent = %v", err)
	}
	res, err := lead.SwitchAgentAccount(ctx, a.Name, api.AgentAccountRequest{Account: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Account != "work" || res.Tool != "Claude Code" || !res.Resumes || !strings.Contains(res.Note, "resumes this conversation") {
		t.Errorf("switch_agent_account = %+v", res)
	}
	waitFor(t, "the agent on work", func() bool { return tool.launchedOn(a.Name) == "default,work" })
	if stored, err := d.srv.store.Agent(ctx, a.Project, a.Name); err != nil || stored.ClaudeAccount != "work" {
		t.Errorf("stored = %+v, %v", stored, err)
	}

	// The composer: the one tool, with every account it may use.
	runners, err := d.client.AgentRunners(ctx, a.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if runners.Tool != "claude" || runners.Account != "work" || len(runners.Runners) != 1 || len(runners.Runners[0].Accounts) != 2 {
		t.Errorf("AgentRunners = %+v", runners)
	}
	if _, err := d.client.SetAgentRunner(ctx, a.Ref(), api.LeadRunnerRequest{Tool: "codex"}); err == nil || !strings.Contains(err.Error(), "keeps its AI tool") {
		t.Errorf("another tool from the composer = %v", err)
	}
	if _, err := d.client.SetAgentRunner(ctx, a.Ref(), api.LeadRunnerRequest{Tool: "claude", Account: "default"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent back on default", func() bool { return tool.launchedOn(a.Name) == "default,work,default" })
}
