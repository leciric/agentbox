package agent_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/credentials"
	"agentbox/internal/gitrepo"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

func TestParseRef(t *testing.T) {
	project, name, err := agent.ParseRef("pawly/agent-01")
	if err != nil || project != "pawly" || name != "agent-01" {
		t.Errorf("ParseRef(pawly/agent-01) = %q, %q, %v", project, name, err)
	}
	for _, bad := range []string{"pawly", "/agent-01", "pawly/", "a/b/c"} {
		if _, _, err := agent.ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) succeeded", bad)
		}
	}
}

// fakeIncus stands in for the incus binary with a shell script.
func fakeIncus(t *testing.T, script string) incus.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "incus")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return incus.Client{Bin: path}
}

type fixture struct {
	m    *agent.Manager
	st   *state.Store
	repo gitrepo.Repo
}

func setup(t *testing.T, inc incus.Client) fixture {
	t.Helper()
	root := testutil.FixtureRepo(t, "hello-stack")
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.AddProject(context.Background(), state.Project{Name: "hello-stack", Root: root, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		m: &agent.Manager{
			Store:   st,
			Incus:   inc,
			Paths:   paths.Paths{Config: t.TempDir(), Data: t.TempDir()},
			Creds:   credentials.Store{Dir: t.TempDir()},
			Secrets: secrets.Store{State: st, KeyPath: filepath.Join(t.TempDir(), "secrets.key")},
			User:    image.User{Name: "dev", UID: 1000, GID: 1000},
			Log:     io.Discard,
		},
		st:   st,
		repo: repo,
	}
}

// TestCreateUsesTheProjectsBranchPrefix makes an agent in a project that
// names its branches thiago/agentbox/, in a repository where agent-01 is taken
// by a local branch and agent-02 by a colleague's pushed one.
func TestCreateUsesTheProjectsBranchPrefix(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) git -C "$ROOT" rev-parse --verify --quiet refs/heads/thiago/agentbox/agent-03 >/dev/null &&
        echo "Error: simulated copy failure, with the branch made" >&2; exit 1 ;;
esac`))
	root := f.repo.Root
	t.Setenv("ROOT", root)
	ctx := context.Background()
	if err := f.st.SetProjectBranchPrefix(ctx, "hello-stack", "thiago/agentbox/"); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "branch", "thiago/agentbox/agent-01")
	testutil.Git(t, root, "remote", "add", "origin", "https://example.invalid/repo.git")
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/thiago/agentbox/agent-02", "main")
	// Another prefix's branches don't take a name in this one.
	testutil.Git(t, root, "branch", "agentbox/agent-03")

	_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err == nil || !strings.Contains(err.Error(), "with the branch made") {
		t.Fatalf("Create() error = %v, want the copy to find thiago/agentbox/agent-03", err)
	}
	if !strings.Contains(err.Error(), "hello-stack/agent-03") {
		t.Errorf("Create() should skip agent-01 and agent-02, whose branches exist here and on origin: %v", err)
	}
}

func TestCreateRollsBackOnFailure(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: simulated copy failure" >&2; exit 1 ;;
esac`))
	if err := os.WriteFile(filepath.Join(f.repo.Root, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo.Root, "branch", "agentbox/agent-01") // left over by an earlier agent

	_, err := f.m.Create(context.Background(), "hello-stack", agent.CreateOptions{AI: "none", CopyEnv: true})
	if err == nil || !strings.Contains(err.Error(), "simulated copy failure") {
		t.Fatalf("Create() error = %v, want the simulated failure", err)
	}
	if !strings.Contains(err.Error(), "hello-stack/agent-02") {
		t.Errorf("Create() should skip agent-01, whose branch exists: %v", err)
	}
	if agents, _ := f.st.Agents(context.Background(), ""); len(agents) != 0 {
		t.Errorf("agents left after rollback: %+v", agents)
	}
	if f.repo.BranchExists("agentbox/agent-02") {
		t.Error("branch agentbox/agent-02 left after rollback")
	}
	if !f.repo.BranchExists("agentbox/agent-01") {
		t.Error("rollback deleted the unrelated agentbox/agent-01 branch")
	}
	if _, err := os.Stat(f.m.Paths.Worktree("hello-stack", "agent-02")); !os.IsNotExist(err) {
		t.Errorf("worktree left after rollback (stat: %v)", err)
	}
}

// TestCreateSeedsClaudeChatDefaults checks what a new Claude Code agent's
// stored chat settings are, once it exists: the ones a fresh session applies
// (see chat.TestNewAgentStartsOnItsDefaults), not the tool's own defaults.
// build() only seeds them for AI "claude" (agent.go); other tests already
// cover an otherwise-identical Create() leaving no chat row at all.
func TestCreateSeedsClaudeChatDefaults(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	if err := f.m.Creds.SaveClaudeToken("dev", "sk-ant-oat01-dev"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	claudeAgent, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := f.st.Chat(ctx, claudeAgent.Project, claudeAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Options["model"] != "opus" || chat.Options["effort"] != "high" {
		t.Errorf("a new Claude Code agent's chat defaults = %+v", chat.Options)
	}
	if _, chosen := chat.Options[state.ChatOptionContextWindow]; chosen {
		t.Errorf("nobody chose a context window, so none should be stored: %+v", chat.Options)
	}

	// A context window is checked against the model before anything is built
	// (D91): Haiku has no 1M window, and a Codex agent has no such setting.
	haiku, oneM := "haiku", "1m"
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", Model: &haiku, ContextWindow: &oneM}); err == nil ||
		!strings.Contains(err.Error(), "haiku has no 1M context window") {
		t.Errorf("1m for haiku: err = %v, want it refused", err)
	}
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "codex", ContextWindow: &oneM}); err == nil {
		t.Error("a Codex agent was given a context window")
	}
}

// TestCreateSeedsTheChosenContextWindow checks that a window chosen for one
// agent is what its chat compacts at.
func TestCreateSeedsTheChosenContextWindow(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	if err := f.m.Creds.SaveClaudeToken("dev", "sk-ant-oat01-dev"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oneM := "1M"
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ContextWindow: &oneM})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := f.st.Chat(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Options[state.ChatOptionContextWindow] != "1000000" {
		t.Errorf("the chosen window was stored as %+v", chat.Options)
	}
}

// TestCreateSeedsTheChosenDefaultModel checks the model picked on the
// overview is what a new Claude Code agent actually starts on, and that
// agents made before it changed keep what they already had.
func TestCreateSeedsTheChosenDefaultModel(t *testing.T) {
	// Two agents are made here, so both need an address.
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	if err := f.m.Creds.SaveClaudeToken("dev", "sk-ant-oat01-dev"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// An agent made before anything was chosen gets AgentBox's own default.
	first, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}

	if err := f.st.SetSetting(ctx, state.SettingDefaultClaudeModel, "haiku"); err != nil {
		t.Fatal(err)
	}
	second, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := f.st.Chat(ctx, second.Project, second.Name)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Options["model"] != "haiku" {
		t.Errorf("a new agent's model = %q, want the chosen default %q", chat.Options["model"], "haiku")
	}
	if chat.Options["effort"] != state.DefaultClaudeEffort {
		t.Errorf("choosing a model shouldn't disturb the effort: %+v", chat.Options)
	}

	// The agent that already existed is left where it was.
	was, err := f.st.Chat(ctx, first.Project, first.Name)
	if err != nil {
		t.Fatal(err)
	}
	if was.Options["model"] != state.DefaultClaudeModel {
		t.Errorf("an agent made earlier moved to %q; existing agents keep their model", was.Options["model"])
	}
}

// TestCreateTakesTheChoicesMadeForOneAgent checks the fallback that decides a
// new agent's chat settings, field by field: what was chosen for this agent,
// then what new agents start on, then AgentBox's own default. A model chosen
// for one agent must not decide its effort as well.
func TestCreateTakesTheChoicesMadeForOneAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}},{"name":"ab-hello-stack-agent-03","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.7"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	if err := f.m.Creds.SaveClaudeToken("dev", "sk-ant-oat01-dev"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	options := func(a state.Agent) map[string]string {
		t.Helper()
		chat, err := f.st.Chat(ctx, a.Project, a.Name)
		if err != nil {
			t.Fatal(err)
		}
		return chat.Options
	}
	choice := func(v string) *string { return &v }

	// Nothing chosen anywhere: AgentBox's own defaults.
	plain, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(plain); got["model"] != state.DefaultClaudeModel || got["effort"] != state.DefaultClaudeEffort {
		t.Errorf("with nothing chosen: %+v, want AgentBox's own defaults", got)
	}

	// What new agents start on, once it is set.
	for key, value := range map[string]string{
		state.SettingDefaultClaudeModel:  "sonnet",
		state.SettingDefaultClaudeEffort: "low",
	} {
		if err := f.st.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}

	// A model chosen for this one agent wins, and leaves the effort on the
	// setting: the two fall back on their own.
	chosen, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", Model: choice("haiku")})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(chosen); got["model"] != "haiku" || got["effort"] != "low" {
		t.Errorf("with a model chosen for the agent: %+v, want haiku at the low the setting asks for", got)
	}

	// And an effort chosen on its own leaves the model on the setting.
	harder, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", Effort: choice("max")})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(harder); got["model"] != "sonnet" || got["effort"] != "max" {
		t.Errorf("with only an effort chosen: %+v, want the setting's sonnet at max", got)
	}
}

// TestChatChoicesRefusesWhatCouldNotApply checks the choices that are turned
// down when an agent is made, rather than stored where nothing reads them.
func TestChatChoicesRefusesWhatCouldNotApply(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	ctx := context.Background()
	choice := func(v string) *string { return &v }
	menu := `[{"value":"default"},{"value":"low"},{"value":"high"},{"value":"xhigh"}]`

	for _, tc := range []struct {
		name          string
		ai            string
		model, effort *string
		menu          string
		wantErr       string
	}{
		{name: "nothing chosen", ai: "claude"},
		{name: "a model for a Codex agent", ai: "codex", model: choice("haiku"), wantErr: "Claude Code settings"},
		{name: "an effort for a shell-only agent", ai: "none", effort: choice("low"), wantErr: "no AI tool"},
		{name: "an empty model", ai: "claude", model: choice(""), wantErr: "an empty model isn't a choice"},
		{name: "an empty effort", ai: "claude", effort: choice("  "), wantErr: "an empty effort isn't a choice"},
		{name: "an effort Claude Code offers", ai: "claude", effort: choice("high"), menu: menu},
		{name: "an effort it has never offered", ai: "claude", effort: choice("ultra"), menu: menu, wantErr: `doesn't offer the effort "ultra"`},
		// A model outside the menu is deliberately let through: the adapter
		// resolves a preference itself, and one it really refuses is reported
		// in the agent's own chat (D45).
		{name: "a model outside the menu", ai: "claude", model: choice("opus[1m]"), menu: menu},
		// Nothing to check against on an install whose chat has never started.
		{name: "an unknown effort with no menu yet", ai: "claude", effort: choice("ultra")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.st.SetSetting(ctx, state.SettingClaudeEffortChoices, tc.menu); err != nil {
				t.Fatal(err)
			}
			err := f.m.ChatChoices(ctx, tc.ai, tc.model, tc.effort)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("refused a usable choice: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("accepted a choice that cannot apply; want an error about %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestCreateUsesProjectBase(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query)
    case "$2" in
      */ab-hello-stack-base/snapshots) echo '["/1.0/instances/ab-hello-stack-base/snapshots/ready"]' ;;
      */ab-hello-stack-base) echo '{"config": {"user.agentbox.saved-from": "hello-stack/agent-01", "user.agentbox.saved-at": "2026-09-12T10:00:00Z"}}' ;;
      *) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
    esac ;;
  copy) echo "Error: stop after copy from $2" >&2; exit 1 ;;
esac`))
	ctx := context.Background()

	base, ok, err := f.m.ProjectBase(ctx, "hello-stack")
	if err != nil || !ok || base.SavedFrom != "hello-stack/agent-01" || base.SavedAt.Year() != 2026 {
		t.Fatalf("ProjectBase() = %+v, %v, %v", base, ok, err)
	}
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"}); err == nil ||
		!strings.Contains(err.Error(), "copy from ab-hello-stack-base/ready") {
		t.Errorf("Create() should copy the project base: %v", err)
	}
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none", Clean: true}); err == nil ||
		!strings.Contains(err.Error(), "copy from agentbox-base/ready") {
		t.Errorf("Create(Clean) should copy the base image: %v", err)
	}
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none", Name: "base"}); err == nil ||
		!strings.Contains(err.Error(), "reserved") {
		t.Errorf("Create(Name: base) should be refused: %v", err)
	}
}

func TestSnapshotNames(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}
	for _, name := range []string{"Bad Name", "rm", "base-1", "fork-1", "-x", strings.Repeat("a", 64)} {
		if _, err := f.m.Snapshot(context.Background(), a, name, false); err == nil || !strings.Contains(err.Error(), "invalid snapshot name") {
			t.Errorf("Snapshot(%q) error = %v, want an invalid-name error", name, err)
		}
	}
}

func TestForkNeedsSnapshot(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	src := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "none"}
	if _, err := f.m.Fork(context.Background(), src, agent.ForkOptions{Snapshot: "missing"}); err == nil || !strings.Contains(err.Error(), `has no snapshot "missing"`) {
		t.Errorf("Fork() from a missing snapshot: got %v", err)
	}
	if _, err := f.m.Fork(context.Background(), src, agent.ForkOptions{Name: "base"}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("Fork() to the reserved name: got %v", err)
	}
}

func TestClaudeAccountResolution(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	ctx := context.Background()
	p, err := f.st.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.ClaudeAccountFor(p, ""); err == nil || !strings.Contains(err.Error(), "agentbox auth claude") {
		t.Errorf("with no account stored: %v", err)
	}
	for _, account := range []string{"personal", "work"} {
		if err := f.m.Creds.SaveClaudeToken(account, "sk-ant-oat01-"+account); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing asked for and nothing on the project: the machine's default.
	if got, err := f.m.ClaudeAccountFor(p, ""); got != "personal" || err != nil {
		t.Errorf("default account = %q, %v", got, err)
	}
	// The project's account, then the one asked for, each win over the previous.
	if err := f.st.SetProjectClaudeAccount(ctx, p.Name, "work"); err != nil {
		t.Fatal(err)
	}
	if p, err = f.st.Project(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.m.ClaudeAccountFor(p, ""); got != "work" || err != nil {
		t.Errorf("the project's account = %q, %v", got, err)
	}
	if got, err := f.m.ClaudeAccountFor(p, "personal"); got != "personal" || err != nil {
		t.Errorf("the account asked for = %q, %v", got, err)
	}
	if _, err := f.m.ClaudeAccountFor(p, "gone"); err == nil ||
		!strings.Contains(err.Error(), `no Claude Code account named "gone"`) ||
		!strings.Contains(err.Error(), "personal") || !strings.Contains(err.Error(), "work") {
		t.Errorf("an unknown account: %v, want it to list personal and work", err)
	}
	// Only Claude Code agents have one.
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "codex", ClaudeAccount: "work"}); err == nil || !strings.Contains(err.Error(), "only applies to agents that run Claude Code") {
		t.Errorf("a Claude account for a Codex agent: %v", err)
	}
}

// TestClaudeAccountAllowList: a project limited to some accounts has
// every level of the resolution refused outside that list, the machine's
// default included, and the error names the accounts it may use. Moving an
// agent and a fork go through the same ClaudeAccountFor, and so does the lead.
func TestClaudeAccountAllowList(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	ctx := context.Background()
	for _, account := range []string{"personal", "spare", "work"} {
		if err := f.m.Creds.SaveClaudeToken(account, "sk-ant-oat01-"+account); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.SetProjectClaudeAccounts(ctx, "hello-stack", "", []string{"work", "spare"}); err != nil {
		t.Fatal(err)
	}
	p, err := f.st.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "may only use the Claude Code accounts work, spare") {
			t.Errorf("%s: %v, want it refused naming work and spare", what, err)
		}
	}
	// The machine's default, personal, is outside the list.
	_, err = f.m.ClaudeAccountFor(p, "")
	refused("the machine's default", err)
	_, err = f.m.ClaudeAccountFor(p, "personal")
	refused("an account asked for", err)
	if got, err := f.m.ClaudeAccountFor(p, "spare"); got != "spare" || err != nil {
		t.Errorf("an allowed account = %q, %v", got, err)
	}
	// An unknown name is still "no such account", not "not allowed".
	if _, err := f.m.ClaudeAccountFor(p, "gone"); err == nil || !strings.Contains(err.Error(), `no Claude Code account named "gone"`) {
		t.Errorf("an unknown account: %v", err)
	}
	_, err = f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ClaudeAccount: "personal"})
	refused("creating an agent", err)
	_, err = f.m.EnsureLead(ctx, "hello-stack")
	refused("the lead", err)

	if err := f.st.SetProjectClaudeAccount(ctx, "hello-stack", "work"); err != nil {
		t.Fatal(err)
	}
	if p, err = f.st.Project(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.m.ClaudeAccountFor(p, ""); got != "work" || err != nil {
		t.Errorf("the project's account = %q, %v", got, err)
	}
}

func TestGitHubAccountResolution(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	ctx := context.Background()
	p, err := f.st.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}

	// Unlike Claude Code, GitHub is optional: nothing stored is not an error.
	if got, err := f.m.GitHubAccountFor(p, ""); got != "" || err != nil {
		t.Errorf("with no account stored: %q, %v", got, err)
	}
	for _, account := range []string{"personal", "work"} {
		if err := f.m.Creds.SaveGitHubToken(account, "gho_"+account); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing asked for and nothing on the project: the machine's default.
	if got, err := f.m.GitHubAccountFor(p, ""); got != "personal" || err != nil {
		t.Errorf("default account = %q, %v", got, err)
	}
	// The project's account, then the one asked for, each win over the previous.
	if err := f.st.SetProjectGitHubAccount(ctx, p.Name, "work"); err != nil {
		t.Fatal(err)
	}
	if p, err = f.st.Project(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.m.GitHubAccountFor(p, ""); got != "work" || err != nil {
		t.Errorf("the project's account = %q, %v", got, err)
	}
	if got, err := f.m.GitHubAccountFor(p, "personal"); got != "personal" || err != nil {
		t.Errorf("the account asked for = %q, %v", got, err)
	}
	if _, err := f.m.GitHubAccountFor(p, "gone"); err == nil || !strings.Contains(err.Error(), `no GitHub account named "gone"`) {
		t.Errorf("an unknown account: %v", err)
	}
}

func TestCreateChecksPrerequisites(t *testing.T) {
	f := setup(t, fakeIncus(t, `[ "$1" = query ] && echo '[]'; exit 0`))
	ctx := context.Background()
	for _, c := range []struct {
		opts agent.CreateOptions
		want string
	}{
		{agent.CreateOptions{AI: "gpt"}, "unknown AI tool"},
		{agent.CreateOptions{AI: "claude"}, "agentbox auth claude"},
		{agent.CreateOptions{AI: "codex"}, "agentbox auth codex"},
		{agent.CreateOptions{AI: "none", Name: "Bad_Name"}, "invalid agent name"},
		{agent.CreateOptions{AI: "none"}, "agentbox image build"},
	} {
		if _, err := f.m.Create(ctx, "hello-stack", c.opts); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Create(%+v) error = %v, want it to mention %q", c.opts, err, c.want)
		}
	}
}

// TestCreateValidatesAndStoresFinishNotice checks create_agent's per-agent
// finish notice: an unknown value is refused before anything is copied, and a
// valid one, or none at all, is stored on the agent as given.
func TestCreateValidatesAndStoresFinishNotice(t *testing.T) {
	f := setup(t, fakeIncus(t, createScript))
	ctx := context.Background()

	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none", FinishNotice: "quietly"}); err == nil ||
		!strings.Contains(err.Error(), `unknown finish notice "quietly"`) {
		t.Errorf("Create() with an unknown finish notice = %v, want it refused", err)
	}

	off, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none", FinishNotice: state.FinishNoticesOff})
	if err != nil {
		t.Fatal(err)
	}
	if off.FinishNotice != state.FinishNoticesOff {
		t.Errorf("agent's FinishNotice = %q, want %q", off.FinishNotice, state.FinishNoticesOff)
	}
	stored, err := f.st.Agent(ctx, "hello-stack", off.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.FinishNotice != state.FinishNoticesOff {
		t.Errorf("stored agent's FinishNotice = %q, want %q", stored.FinishNotice, state.FinishNoticesOff)
	}

	unset, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if unset.FinishNotice != "" {
		t.Errorf("agent's FinishNotice with none given = %q, want empty", unset.FinishNotice)
	}
}

// TestCreateTakesTheProjectsModel checks the model step a project adds to the
// chain: what was chosen for this agent, then the project's own model, then
// what new agents start on, then AgentBox's own default. A project on auto
// names no model — its chat chooses one per agent — so an agent created
// without one falls back exactly as it does in a project that asks for nothing.
func TestCreateTakesTheProjectsModel(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}},{"name":"ab-hello-stack-agent-03","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.7"}]}}}},{"name":"ab-hello-stack-agent-04","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.8"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	if err := f.m.Creds.SaveClaudeToken("dev", "sk-ant-oat01-dev"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	options := func(a state.Agent) map[string]string {
		t.Helper()
		chat, err := f.st.Chat(ctx, a.Project, a.Name)
		if err != nil {
			t.Fatal(err)
		}
		return chat.Options
	}
	if err := f.st.SetSetting(ctx, state.SettingDefaultClaudeModel, "sonnet"); err != nil {
		t.Fatal(err)
	}

	// Nothing said for the project: what new agents start on, as before.
	follows, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(follows); got["model"] != "sonnet" {
		t.Errorf("with the project naming no model: %+v, want the installation's sonnet", got)
	}

	// The project's own model beats the installation's setting.
	if err := f.st.SetProjectAgentModel(ctx, "hello-stack", "haiku"); err != nil {
		t.Fatal(err)
	}
	theProjects, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(theProjects); got["model"] != "haiku" {
		t.Errorf("with the project on haiku: %+v, want haiku", got)
	}
	if got := options(theProjects); got["effort"] != state.DefaultClaudeEffort {
		t.Errorf("a project's model shouldn't disturb the effort: %+v", got)
	}

	// And a model chosen for one agent beats the project's.
	chosen := "opus"
	forThisOne, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", Model: &chosen})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(forThisOne); got["model"] != "opus" {
		t.Errorf("with a model chosen for the agent: %+v, want opus", got)
	}

	// On auto the chat chooses per agent, and an agent it chose nothing for
	// falls back to the installation's setting rather than to "auto".
	if err := f.st.SetProjectAgentModel(ctx, "hello-stack", state.AgentModelAuto); err != nil {
		t.Fatal(err)
	}
	unchosen, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(unchosen); got["model"] != "sonnet" {
		t.Errorf("on auto with no model chosen: %+v, want the installation's sonnet", got)
	}
}

// codexLogin gives AgentBox the Codex login an agent needs, so a Create gets
// past the login check and on to the image check this test is about.
func codexLogin(t *testing.T, f fixture) {
	t.Helper()
	path := f.m.Creds.CodexAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tokens": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Codex is optional in the base image, so an agent asking for it on an image
// built without it is refused before anything is copied, with the words Setup
// uses for the same thing.
func TestCreateChecksCodexIsInTheImage(t *testing.T) {
	base := func(codex string) incus.Client {
		return fakeIncus(t, `case "$1" in
  query)
    case "$2" in
      */snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      *) echo '{"config": {"user.agentbox.with-codex": "`+codex+`"}, "devices": {}}' ;;
    esac ;;
  list) echo '[{"name": "agentbox-base", "config": {"user.agentbox.with-codex": "`+codex+`"}}]' ;;
  copy) echo "Error: copied, which is as far as this test goes" >&2; exit 1 ;;
esac
exit 0`)
	}
	ctx := context.Background()

	f := setup(t, base("0"))
	codexLogin(t, f)
	_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "codex"})
	if err == nil || !strings.Contains(err.Error(), image.CodexMissing) {
		t.Errorf("Codex on an image without it: %v", err)
	}
	// A tool the image always has is not refused, and neither is Codex once
	// the image has been built with it.
	f = setup(t, base("1"))
	codexLogin(t, f)
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "codex"}); err == nil || strings.Contains(err.Error(), image.CodexMissing) {
		t.Errorf("Codex on an image with it: %v", err)
	}
}

// openCodeLogin gives AgentBox the OpenCode login an agent needs, so a Create
// gets past the login check and on to whatever the test is really about.
func openCodeLogin(t *testing.T, f fixture) {
	t.Helper()
	path := f.m.Creds.OpenCodeAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"anthropic": {"type": "api", "key": "sk-test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// OpenCode is optional in the base image and in the credentials, exactly as
// Codex is, and both are checked before anything is copied.
func TestCreateChecksOpenCodeIsInTheImage(t *testing.T) {
	base := func(opencode string) incus.Client {
		return fakeIncus(t, `case "$1" in
  query)
    case "$2" in
      */snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      *) echo '{"config": {"user.agentbox.with-opencode": "`+opencode+`"}, "devices": {}}' ;;
    esac ;;
  list) echo '[{"name": "agentbox-base", "config": {"user.agentbox.with-opencode": "`+opencode+`"}}]' ;;
  copy) echo "Error: copied, which is as far as this test goes" >&2; exit 1 ;;
esac
exit 0`)
	}
	ctx := context.Background()

	// No login at all: that is the first thing said, and it names the command.
	f := setup(t, base("1"))
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "opencode"}); err == nil || !strings.Contains(err.Error(), "agentbox auth opencode") {
		t.Errorf("OpenCode with no login: %v", err)
	}

	f = setup(t, base("0"))
	openCodeLogin(t, f)
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "opencode"}); err == nil || !strings.Contains(err.Error(), image.OpenCodeMissing) {
		t.Errorf("OpenCode on an image without it: %v", err)
	}
	f = setup(t, base("1"))
	openCodeLogin(t, f)
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "opencode"}); err == nil || strings.Contains(err.Error(), image.OpenCodeMissing) {
		t.Errorf("OpenCode on an image with it: %v", err)
	}
}

// OpenCodeReady is both halves at once: the image and the login. Either one
// missing means no OpenCode agents, which is what the lead and the app read.
func TestOpenCodeReady(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	ctx := context.Background()
	if ready, err := f.m.OpenCodeReady(ctx); ready || err != nil {
		t.Errorf("with neither the image nor a login: %v, %v", ready, err)
	}
	openCodeLogin(t, f)
	if ready, err := f.m.OpenCodeReady(ctx); ready || err != nil {
		t.Errorf("with a login but no image: %v, %v", ready, err)
	}
	if err := f.st.SetFlag(ctx, state.SettingImageOpenCode, true); err != nil {
		t.Fatal(err)
	}
	if ready, err := f.m.OpenCodeReady(ctx); !ready || err != nil {
		t.Errorf("with both: %v, %v", ready, err)
	}
}

// An OpenCode agent's model is its own: stored for the chat to apply when the
// session starts, and never mixed up with Claude Code's settings. OpenCode has
// no effort at all, and no installation-wide model either — only what was
// chosen for this one agent is stored.
func TestOpenCodeAgentModelIsStored(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {"user.agentbox.with-opencode": "1"}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	ctx := context.Background()
	choice := func(v string) *string { return &v }
	openCodeLogin(t, f)
	options := func(a state.Agent) map[string]string {
		t.Helper()
		chat, err := f.st.Chat(ctx, a.Project, a.Name)
		if err != nil {
			t.Fatal(err)
		}
		return chat.Options
	}

	// The model chosen for this agent is what its chat starts on.
	chosen, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "opencode", Model: choice("anthropic/claude-sonnet-5")})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(chosen); got["model"] != "anthropic/claude-sonnet-5" || got["effort"] != "" {
		t.Errorf("with an OpenCode model chosen: %+v", got)
	}
	// Claude Code's own default model is never seeded into an OpenCode agent:
	// "opus[1m]" is not a provider/model id and OpenCode would refuse it.
	if err := f.st.SetSetting(ctx, state.SettingDefaultClaudeModel, "sonnet"); err != nil {
		t.Fatal(err)
	}
	plain, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(plain); len(got) != 0 {
		t.Errorf("with nothing chosen: %+v, want no Claude Code settings at all", got)
	}

	if err := f.m.ChatChoices(ctx, "opencode", choice("anthropic/claude-sonnet-5"), nil); err != nil {
		t.Errorf("an OpenCode model: %v", err)
	}
	if err := f.m.ChatChoices(ctx, "opencode", choice(""), nil); err == nil || !strings.Contains(err.Error(), "an empty model isn't a choice") {
		t.Errorf("an empty OpenCode model: %v", err)
	}
	if err := f.m.ChatChoices(ctx, "opencode", nil, choice("high")); err == nil || !strings.Contains(err.Error(), "Claude Code setting") {
		t.Errorf("an effort for an OpenCode agent: %v", err)
	}
}
