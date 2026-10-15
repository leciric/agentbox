package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// TestChatRunnersOfferWhatIsSignedIn walks the composer's picker: every tool
// is listed, only those AgentBox has a login for can be chosen, Claude Code
// lists only the accounts the project may use, and a choice made before the
// chat has a session applies at once.
func TestChatRunnersOfferWhatIsSignedIn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work", "other"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.UpdateProject(ctx, p.Name, api.UpdateProjectRequest{ClaudeAccounts: &[]string{"default", "work"}}); err != nil {
		t.Fatal(err)
	}

	runners, err := d.client.ChatRunners(ctx, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if runners.Tool != "claude" || runners.Account != "default" || len(runners.Runners) != 4 {
		t.Fatalf("runners = %+v, want Claude Code on default, four tools", runners)
	}
	byTool := map[string]api.LeadRunner{}
	for _, r := range runners.Runners {
		byTool[r.Tool] = r
	}
	if !byTool["claude"].Ready || byTool["codex"].Ready || !strings.Contains(byTool["codex"].Reason, "agentbox auth codex") {
		t.Errorf("claude %+v, codex %+v: want Claude Code ready, Codex not, saying how to sign in", byTool["claude"], byTool["codex"])
	}
	var names []string
	for _, a := range byTool["claude"].Accounts {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "default,work" {
		t.Errorf("Claude Code's accounts = %v, want the two the project allows", names)
	}

	if _, err := d.client.SetChatRunner(ctx, p.Name, api.LeadRunnerRequest{Tool: "codex"}); err == nil || !strings.Contains(err.Error(), "Codex login") {
		t.Errorf("switching to Codex with no login: %v, want it refused", err)
	}
	if _, err := d.client.SetChatRunner(ctx, p.Name, api.LeadRunnerRequest{Tool: "claude", Account: "other"}); err == nil || !strings.Contains(err.Error(), "may only use") {
		t.Errorf("switching to an account the project doesn't allow: %v, want it refused", err)
	}
	session, err := d.client.SetChatRunner(ctx, p.Name, api.LeadRunnerRequest{Tool: "claude", Account: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if session.Tool != "claude" || session.NextTool != "" {
		t.Errorf("session = %+v, want Claude Code, nothing pending", session)
	}
	if runners, err = d.client.ChatRunners(ctx, p.Name); err != nil || runners.Chosen != "work" || runners.Account != "work" {
		t.Errorf("after choosing work: %+v, %v; want it chosen and resolved", runners, err)
	}

	// Signed in to Codex, the chat switches; with no session yet, at once.
	if err := creds.SaveToolAccount("codex", "", "", []byte(`{"tokens":{}}`)); err != nil {
		t.Fatal(err)
	}
	if session, err = d.client.SetChatRunner(ctx, p.Name, api.LeadRunnerRequest{Tool: "codex"}); err != nil {
		t.Fatal(err)
	}
	if session.Tool != "codex" || session.Account != "default" {
		t.Errorf("session = %+v, want Codex now, on its default account", session)
	}
	stored, err := d.srv.store.Chat(ctx, p.Name, state.LeadName)
	if err != nil || stored.Tool != "codex" || stored.Account != "work" {
		t.Errorf("stored %+v, %v: want codex, with Claude Code's account kept for a switch back", stored, err)
	}

	// A second Codex account is offered, and choosing it moves the chat to it.
	if err := creds.SaveToolAccount("codex", "", "team", []byte(`{"OPENAI_API_KEY":"sk"}`)); err != nil {
		t.Fatal(err)
	}
	if runners, err = d.client.ChatRunners(ctx, p.Name); err != nil {
		t.Fatal(err)
	}
	var codex api.LeadRunner
	for _, r := range runners.Runners {
		if r.Tool == "codex" {
			codex = r
		}
	}
	if len(codex.Accounts) != 2 || !codex.Accounts[0].Project || runners.ToolAccounts["codex"] != "default" {
		t.Errorf("Codex's accounts = %+v, running on %v", codex.Accounts, runners.ToolAccounts)
	}
	if _, err := d.client.SetChatRunner(ctx, p.Name, api.LeadRunnerRequest{Tool: "codex", Account: "nope"}); err == nil {
		t.Error("a Codex account that doesn't exist was chosen")
	}
	if session, err = d.client.SetChatRunner(ctx, p.Name, api.LeadRunnerRequest{Tool: "codex", Account: "team"}); err != nil || session.Account != "team" {
		t.Errorf("choosing the team account: %+v, %v", session, err)
	}
	if runners, err = d.client.ChatRunners(ctx, p.Name); err != nil || runners.ToolChosen["codex"] != "team" || runners.ToolAccounts["codex"] != "team" {
		t.Errorf("after choosing team: %+v, %v", runners, err)
	}

	// The Home chat has the same picker, against the machine's accounts.
	if runners, err = d.client.ChatRunners(ctx, state.HomeProject); err != nil || runners.Tool != "claude" || len(runners.Runners[0].Accounts) != 3 {
		t.Errorf("Home's runners = %+v, %v; want Claude Code with every account", runners, err)
	}
}
