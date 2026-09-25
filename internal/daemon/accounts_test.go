package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// readyOneAgentIncus is fakeIncus's base-image check plus oneAgentIncus's
// list, so a create job finds the image ready and its instance already
// running with an address, instead of waiting out a real Incus copy.
const readyOneAgentIncus = `case "$1" in
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running", "state": {"network": {"eth0": {"addresses": [{"family": "inet", "address": "10.1.2.3"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  delete) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0
`

// addProjectAllowingEvery adds a project and empties its allowed list, which
// a new project otherwise starts with only its own account on, so it may use
// every account.
func addProjectAllowingEvery(t *testing.T, d testDaemon, repo string) {
	t.Helper()
	ctx := context.Background()
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.UpdateProject(ctx, p.Name, api.UpdateProjectRequest{ClaudeAccounts: &[]string{}}); err != nil {
		t.Fatal(err)
	}
}

// TestNewProjectAllowsItsOwnAccount: a new project starts with only the
// account it was given allowed, or the machine's default when it was given
// none, rather than an empty list, which still allows every account.
func TestNewProjectAllowsItsOwnAccount(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")})
	if err != nil || strings.Join(p.ClaudeAccounts, ",") != "default" {
		t.Errorf("with no account chosen: ClaudeAccounts = %v, %v, want only the machine's default", p.ClaudeAccounts, err)
	}
	p, err = d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), Name: "pawly", ClaudeAccount: "work"})
	if err != nil || p.ClaudeAccount != "work" || strings.Join(p.ClaudeAccounts, ",") != "work" {
		t.Errorf("with work chosen: %q, %v, %v, want only work", p.ClaudeAccount, p.ClaudeAccounts, err)
	}
	// It is the same list create_agent and list_accounts go by.
	lead := api.NewClient(d.srv.leadSocketPath("pawly"))
	if accounts, err := lead.ProjectAccounts(ctx); err != nil || len(accounts) != 1 || accounts[0].Name != "work" {
		t.Errorf("list_accounts = %+v, %v, want only work", accounts, err)
	}
	if _, err := lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders page", Task: "add it", ClaudeAccount: "default"}); err == nil ||
		!strings.Contains(err.Error(), `may only use the Claude Code accounts work, not "default"`) {
		t.Errorf("create_agent on default: %v, want it refused naming work", err)
	}
}

// TestLeadCreateAgentPicksAccount: a project's chat can name one of this
// machine's Claude Code accounts when it creates an agent, and the agent
// keeps it (D88).
func TestLeadCreateAgentPicksAccount(t *testing.T) {
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
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))

	job, err := lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders page", Task: "add it", ClaudeAccount: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
		t.Fatal(err)
	}
	if j, err := d.client.Job(ctx, job.ID); err != nil || j.Status != api.JobSucceeded {
		t.Fatalf("job = %+v, %v", j, err)
	}
	agents, err := d.client.Agents(ctx, "hello-stack")
	if err != nil || len(agents) != 1 {
		t.Fatalf("Agents() = %+v, %v", agents, err)
	}
	if agents[0].ClaudeAccount != "work" {
		t.Errorf("ClaudeAccount = %q, want %q", agents[0].ClaudeAccount, "work")
	}
}

// TestLeadCreateAgentUnknownAccount: an account the chat named that doesn't
// exist is refused before any job starts, so create_agent's own reply names
// the mistake — the chat never hears that an agent is on its way and then
// learns otherwise (see TestLeadCreateAgentAccountFailureNotifiesLead for a
// failure that can only be found once the job is already running).
func TestLeadCreateAgentUnknownAccount(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))

	_, err := lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders page", Task: "add it", ClaudeAccount: "ghost"})
	if err == nil || !strings.Contains(err.Error(), `no Claude Code account named "ghost"`) ||
		!strings.Contains(err.Error(), "default") || !strings.Contains(err.Error(), "work") {
		t.Errorf("CreateProjectAgent() err = %v, want it refused and to list default and work", err)
	}
	agents, err := d.client.Agents(ctx, "hello-stack")
	if err != nil || len(agents) != 0 {
		t.Fatalf("Agents() = %+v, %v, want none made", agents, err)
	}
}

// TestLeadCreateAgentAccountRefusedForNonClaude: naming an account for an
// agent that won't run Claude Code is refused — Codex and OpenCode have a
// single login each, not named accounts — and again before any job starts.
func TestLeadCreateAgentAccountRefusedForNonClaude(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))

	_, err := lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Investigate", Task: "look into it", AI: "opencode", ClaudeAccount: "default"})
	if err == nil || !strings.Contains(err.Error(), "only applies to agents that run Claude Code") {
		t.Errorf("CreateProjectAgent() err = %v, want it refused for running opencode", err)
	}
}

// TestLeadAccountsRoute: a project's chat sees every account, which is the
// machine's default, which is this project's own, how many of its running
// agents hold each one, and the latest usage reading — never a token.
func TestLeadAccountsRoute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, recordingIncus, testConfig{instances: runningAgent01})
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work", "spare"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	repo := d.fixtureRepo(t, "hello-stack")
	addProjectAllowingEvery(t, d, repo)
	if err := d.srv.store.SetProjectClaudeAccount(ctx, "hello-stack", "work"); err != nil {
		t.Fatal(err)
	}
	// A running agent on "work", so it counts, and a reading for "work" only:
	// "spare" has never reported one.
	worktree := d.paths.Worktree("hello-stack", "agent-01")
	commit := testutil.Git(t, repo, "rev-parse", "HEAD")
	testutil.Git(t, repo, "worktree", "add", "--quiet", "-b", "agentbox/agent-01", worktree, commit)
	if err := d.srv.store.AddAgent(ctx, state.Agent{
		Project: "hello-stack", Name: "agent-01", Title: "Reminders page", Instance: "ab-hello-stack-agent-01",
		AI: "claude", ClaudeAccount: "work", Branch: "agentbox/agent-01", BaseRef: "main", BaseCommit: commit,
		Worktree: worktree, Status: state.AgentReady, CreatedAt: time.Now(), Interface: state.InterfaceChat,
	}); err != nil {
		t.Fatal(err)
	}
	d.srv.claudeLimited(state.Agent{Project: "hello-stack", Name: "agent-01", AI: "claude", ClaudeAccount: "work"},
		acp.RateLimit{Status: "allowed", UnifiedWindows: map[string]acp.LimitWindow{
			"five_hour": {Utilization: 0.5, ResetsAt: 1790092800},
		}})

	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	accounts, err := lead.ProjectAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("ProjectAccounts() = %+v, want 3", accounts)
	}
	by := map[string]api.LeadAccount{}
	for _, a := range accounts {
		by[a.Name] = a
	}
	if !by["default"].Default || by["work"].Default {
		t.Errorf("default flags: %+v", by)
	}
	if !by["work"].Project || by["default"].Project || by["spare"].Project {
		t.Errorf("project flags: %+v", by)
	}
	if by["work"].Agents != 1 {
		t.Errorf("work's Agents = %d, want 1", by["work"].Agents)
	}
	if by["work"].Limit == nil || len(by["work"].Limit.Windows) != 1 {
		t.Errorf("work's Limit = %+v, want a reading", by["work"].Limit)
	}
	if by["default"].Limit != nil || by["spare"].Limit != nil {
		t.Errorf("default/spare have no reading yet, got %+v / %+v", by["default"].Limit, by["spare"].Limit)
	}

	// Never a token, anywhere in what a chat can see of its accounts.
	req, err := http.NewRequest(http.MethodGet, "http://agentbox/v1/project/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := lead.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sk-ant-oat01") {
		t.Errorf("a token leaked into the fleet's body:\n%s", body)
	}
	raw, err := json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-ant-oat01") {
		t.Errorf("ProjectAccounts() leaked a token:\n%s", raw)
	}
}

// TestProjectClaudeAccountsAllowList: a project limited to some accounts
// takes the list over the API, refuses one without its own account, refuses a
// create_agent outside it naming the ones it may use, and list_accounts shows
// the lead only those.
func TestProjectClaudeAccountsAllowList(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work", "spare"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.ClaudeAccounts, ",") != "default" {
		t.Fatalf("a new project's ClaudeAccounts = %#v, want only the machine's default", p.ClaudeAccounts)
	}

	work := "work"
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccount: &work}); err == nil {
		t.Error("moving to an account the new project doesn't allow was accepted")
	}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccount: &work, ClaudeAccounts: &[]string{}}); err != nil {
		t.Fatal(err)
	}
	without := []string{"default", "spare"}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccounts: &without}); err == nil ||
		!strings.Contains(err.Error(), `"work"`) {
		t.Errorf("a list without the project's account: %v", err)
	}
	ghost := []string{"work", "ghost"}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccounts: &ghost}); err == nil ||
		!strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("a list naming an account that doesn't exist: %v", err)
	}
	allowed := []string{"work", "default"}
	if p, err = d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccounts: &allowed}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.ClaudeAccounts, ",") != "work,default" {
		t.Errorf("ClaudeAccounts = %v", p.ClaudeAccounts)
	}

	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	accounts, err := lead.ProjectAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range accounts {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "default,work" {
		t.Errorf("list_accounts shows %v, want default and work only", names)
	}

	// Refused synchronously, before a job is even started: create_agent's own
	// reply says so, rather than a job the chat is told has started and only
	// later hears otherwise (the bug this guards is the lead being told an
	// agent exists when the job failed at once).
	_, err = lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders page", Task: "add it", ClaudeAccount: "spare"})
	if err == nil || !strings.Contains(err.Error(), "may only use the Claude Code accounts work, default") {
		t.Errorf("CreateProjectAgent() err = %v, want it refused naming work and default", err)
	}

	none := []string{}
	if p, err = d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccounts: &none}); err != nil || len(p.ClaudeAccounts) != 0 {
		t.Errorf("clearing the list: %v, %v", p.ClaudeAccounts, err)
	}
}

// TestProjectAccountMovesItsChat: agentbox claude-account <project> <account>
// moves the project's chat too, at once, and not only when the allow list
// changes: the chat runs on the account its project resolves to, and the
// Tokens tab reads its limits under the account on its row.
func TestProjectAccountMovesItsChat(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "personal"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	addProjectAllowingEvery(t, d, d.fixtureRepo(t, "hello-stack"))
	m := d.srv.manager(nil)
	if a, err := m.EnsureLead(ctx, "hello-stack"); err != nil || a.ClaudeAccount != "default" {
		t.Fatalf("EnsureLead() = %q, %v; want the default account", a.ClaudeAccount, err)
	}

	personal := "personal"
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccount: &personal}); err != nil {
		t.Fatal(err)
	}
	a, err := m.Lead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if a.ClaudeAccount != "personal" {
		t.Errorf("after the project moved to %q, its chat runs on %q", "personal", a.ClaudeAccount)
	}
}

// TestRenameClaudeAccount: the route renames the token and carries the
// project over, keeps the machine default on the same account, and refuses a
// name that is taken or invalid without touching anything.
func TestRenameClaudeAccount(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), ClaudeAccount: "work"}); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"default", "Not Valid", ""} {
		if _, err := d.client.RenameClaudeAccount(ctx, "work", bad); err == nil {
			t.Errorf("renaming work to %q was allowed", bad)
		}
	}
	if _, err := d.client.RenameClaudeAccount(ctx, "nobody", "someone"); err == nil {
		t.Error("renaming an account that doesn't exist was allowed")
	}
	if p, _ := d.client.Project(ctx, "hello-stack"); p.ClaudeAccount != "work" {
		t.Fatalf("a refused rename moved the project to %q", p.ClaudeAccount)
	}

	got, err := d.client.RenameClaudeAccount(ctx, "work", "client")
	if err != nil {
		t.Fatal(err)
	}
	if got.Old != "work" || got.Name != "client" || len(got.Projects) != 1 || got.Projects[0] != "hello-stack" || got.Agents == nil {
		t.Errorf("RenameClaudeAccount() = %+v", got)
	}
	if p, _ := d.client.Project(ctx, "hello-stack"); p.ClaudeAccount != "client" {
		t.Errorf("the project's account = %q, want client", p.ClaudeAccount)
	}
	if token, _ := creds.ClaudeToken("client"); token != "sk-ant-oat01-work" {
		t.Errorf("client's token = %q", token)
	}

	if _, err := d.client.RenameClaudeAccount(ctx, "default", "personal"); err != nil {
		t.Fatal(err)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var def string
	for _, acc := range auth.ClaudeAccounts {
		if acc.Default {
			def = acc.Name
		}
	}
	if def != "personal" {
		t.Errorf("the default after renaming it = %q, want personal", def)
	}
}

// TestRenameGitHubAccount: the route renames the token and carries the
// project over, keeps the machine default on the same account, and refuses a
// name that is taken or invalid without touching anything.
func TestRenameGitHubAccount(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveGitHubToken(name, "gho_"+name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack"), GitHubAccount: "work"}); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"default", "Not Valid", ""} {
		if _, err := d.client.RenameGitHubAccount(ctx, "work", bad); err == nil {
			t.Errorf("renaming work to %q was allowed", bad)
		}
	}
	if _, err := d.client.RenameGitHubAccount(ctx, "nobody", "someone"); err == nil {
		t.Error("renaming an account that doesn't exist was allowed")
	}
	if p, _ := d.client.Project(ctx, "hello-stack"); p.GitHubAccount != "work" {
		t.Fatalf("a refused rename moved the project to %q", p.GitHubAccount)
	}

	got, err := d.client.RenameGitHubAccount(ctx, "work", "client")
	if err != nil {
		t.Fatal(err)
	}
	if got.Old != "work" || got.Name != "client" || len(got.Projects) != 1 || got.Projects[0] != "hello-stack" || got.Agents == nil {
		t.Errorf("RenameGitHubAccount() = %+v", got)
	}
	if p, _ := d.client.Project(ctx, "hello-stack"); p.GitHubAccount != "client" {
		t.Errorf("the project's account = %q, want client", p.GitHubAccount)
	}
	if token, _ := creds.GitHubToken("client"); token != "gho_work" {
		t.Errorf("client's token = %q", token)
	}

	if _, err := d.client.RenameGitHubAccount(ctx, "default", "personal"); err != nil {
		t.Fatal(err)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var def string
	for _, acc := range auth.GitHubAccounts {
		names = append(names, acc.Name)
		if acc.Default {
			def = acc.Name
		}
	}
	if def != "personal" || strings.Join(names, ",") != "client,personal" {
		t.Errorf("GitHub accounts after the renames = %v, default %q; want client,personal with personal the default", names, def)
	}
}
