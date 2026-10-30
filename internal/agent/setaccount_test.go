package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// toolAgentFixture is claudeAgentFixture's agent-01 on another AI tool.
func toolAgentFixture(t *testing.T, f fixture, ai string, edit func(*state.Agent)) state.Agent {
	t.Helper()
	worktree := f.m.Paths.Worktree("hello-stack", "agent-01")
	if err := f.repo.AddWorktree(worktree, "agentbox/agent-01", "HEAD", "test"); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01",
		Branch: "agentbox/agent-01", Worktree: worktree, AI: ai,
		CreatedAt: time.Now(), Status: state.AgentReady,
	}
	if edit != nil {
		edit(&a)
	}
	if err := f.st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetAgentToolAccounts(context.Background(), a.Project, a.Name, a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestSetAccountMovesARunningCodexAgent checks a Codex agent moving to its
// second account: the new account's login replaces ~/.codex/auth.json in its
// machine, and the row records it.
func TestSetAccountMovesARunningCodexAgent(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()
	for name, auth := range map[string]string{"personal": `{"tokens":{"account_id":"personal"}}`, "work": `{"tokens":{"account_id":"work"}}`} {
		if err := f.m.Creds.SaveToolAccount("codex", "", name, []byte(auth)); err != nil {
			t.Fatal(err)
		}
	}
	a := toolAgentFixture(t, f, "codex", func(a *state.Agent) { a.CodexAccount = "personal" })

	got, err := f.m.SetAccount(ctx, a, agent.Logins{Codex: "work"})
	if err != nil {
		t.Fatalf("SetAccount(work) = %v", err)
	}
	if got.CodexAccount != "work" {
		t.Errorf("SetAccount returned %+v, want CodexAccount work", got)
	}
	if auth := inAgent(t, files, a.Instance, "/home/dev/.codex/auth.json"); !strings.Contains(auth, `"work"`) {
		t.Errorf("auth.json in the machine = %s, want the work account's login", auth)
	}
	stored, err := f.st.Agent(ctx, a.Project, a.Name)
	if err != nil || stored.CodexAccount != "work" {
		t.Errorf("stored = %+v, %v; want CodexAccount work", stored, err)
	}
	if _, err := f.m.SetAccount(ctx, got, agent.Logins{Codex: "nope"}); err == nil || !strings.Contains(err.Error(), `no Codex account named "nope"`) {
		t.Errorf("SetAccount(nope) = %v, want the account refused", err)
	}
}

// TestSetAccountLaysOpenCodeProvidersOverTheAgents checks that naming one
// provider's account keeps the agent's others as they were.
func TestSetAccountLaysOpenCodeProvidersOverTheAgents(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()
	for _, acc := range []struct{ provider, name string }{{"anthropic", "personal"}, {"anthropic", "work"}, {"openai", "team"}} {
		if err := f.m.Creds.SaveToolAccount("opencode", acc.provider, acc.name, []byte(`{"type":"api","key":"sk-`+acc.name+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	a := toolAgentFixture(t, f, "opencode", func(a *state.Agent) {
		a.OpenCodeAccounts = state.AccountMap{"anthropic": "personal", "openai": "team"}
	})
	got, err := f.m.SetAccount(ctx, a, agent.Logins{OpenCode: state.AccountMap{"anthropic": "work"}})
	if err != nil {
		t.Fatalf("SetAccount = %v", err)
	}
	if got.OpenCodeAccounts["anthropic"] != "work" || got.OpenCodeAccounts["openai"] != "team" {
		t.Errorf("OpenCode accounts = %v, want anthropic on work and openai still on team", got.OpenCodeAccounts)
	}
	auth := inAgent(t, files, a.Instance, "/home/dev/.local/share/opencode/auth.json")
	if !strings.Contains(auth, "sk-work") || !strings.Contains(auth, "sk-team") || strings.Contains(auth, "sk-personal") {
		t.Errorf("OpenCode's auth.json = %s, want work's and team's keys only", auth)
	}
}

// TestSetAccountRefusesWhatItCantDeliver: a stopped Codex agent, whose login
// file can't be written; a lead; an account of a tool the agent doesn't run.
func TestSetAccountRefusesWhatItCantDeliver(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  exec) echo "exec shouldn't run on a stopped agent" >&2; exit 1 ;;
esac`))
	ctx := context.Background()
	if err := f.m.Creds.SaveToolAccount("codex", "", "work", []byte(`{"tokens":{}}`)); err != nil {
		t.Fatal(err)
	}
	a := toolAgentFixture(t, f, "codex", nil)
	if _, err := f.m.SetAccount(ctx, a, agent.Logins{Codex: "work"}); err == nil || !strings.Contains(err.Error(), "start it first") {
		t.Errorf("SetAccount on a stopped Codex agent = %v, want it to say to start it", err)
	}
	if stored, _ := f.st.Agent(ctx, a.Project, a.Name); stored.CodexAccount == "work" {
		t.Error("a refused move was stored anyway")
	}
	if _, err := f.m.SetAccount(ctx, a, agent.Logins{Claude: "work"}); err == nil || !strings.Contains(err.Error(), "only applies to agents that run Claude Code") {
		t.Errorf("a Claude Code account for a Codex agent = %v", err)
	}
	lead := a
	lead.Name, lead.Role = state.LeadName, state.RoleLead
	if _, err := f.m.SetAccount(ctx, lead, agent.Logins{}); err == nil || !strings.Contains(err.Error(), "composer") {
		t.Errorf("SetAccount on a lead = %v", err)
	}
}

// TestSetAccountMovesAClaudeAgentAsSetClaudeAccountDoes: Claude Code's goes
// the old way, which a stopped agent may take too (Start writes its env).
func TestSetAccountMovesAClaudeAgentAsSetClaudeAccountDoes(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
esac`))
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	a := claudeAgentFixture(t, f)
	got, err := f.m.SetAccount(ctx, a, agent.Logins{Claude: "work"})
	if err != nil || got.ClaudeAccount != "work" {
		t.Fatalf("SetAccount(work) = %+v, %v", got, err)
	}
	if _, err := f.m.SetAccount(ctx, a, agent.Logins{Codex: "work"}); err == nil || !strings.Contains(err.Error(), "name a Claude Code account") {
		t.Errorf("a Codex account for a Claude Code agent = %v", err)
	}
}
