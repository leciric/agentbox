package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// unsetenv removes an environment variable for the test, restoring whatever
// it was (set or not) afterwards. Unlike t.Setenv(name, ""), which still
// leaves the variable present in os.Environ(), this is for code that only
// checks whether a variable was set at all, not what it was set to.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	if old, ok := os.LookupEnv(name); ok {
		t.Cleanup(func() { _ = os.Setenv(name, old) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(name) })
	}
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

// leadFixture is a project whose Claude Code login exists, so the lead can be
// created. No incus binary is set: a lead must never reach for one.
func leadFixture(t *testing.T) fixture {
	t.Helper()
	f := setup(t, incus.Client{Bin: filepath.Join(t.TempDir(), "no-incus")})
	if err := f.m.Creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestEnsureLeadMakesNoMachineAndNoBranch(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)

	branchesBefore, _ := f.repo.Branches("")
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if !a.IsLead() {
		t.Errorf("EnsureLead() role = %q, want %q", a.Role, state.RoleLead)
	}
	if a.Branch != "" {
		t.Errorf("Branch = %q: the lead commits nothing, so it has no branch", a.Branch)
	}
	if branches, _ := f.repo.Branches(""); !slices.Equal(branches, branchesBefore) {
		t.Errorf("branches = %v, want %v unchanged: the lead makes none", branches, branchesBefore)
	}
	// It reads a real checkout, standing on the project's branch, detached.
	if _, err := os.Stat(filepath.Join(a.Worktree, "server.mjs")); err != nil {
		t.Errorf("the lead's worktree has no project files: %v", err)
	}
	// A detached HEAD has no branch name: rev-parse answers "HEAD".
	if head := testutil.Git(t, a.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
		t.Errorf("HEAD = %q, want it detached", head)
	}
	if at := testutil.Git(t, a.Worktree, "rev-parse", "HEAD"); at != a.BaseCommit {
		t.Errorf("worktree at %s, want %s", at, a.BaseCommit)
	}
	// The main checkout still holds the branch: two worktrees, one branch, no clash.
	if branch := testutil.Git(t, f.repo.Root, "rev-parse", "--abbrev-ref", "HEAD"); branch != a.BaseRef {
		t.Errorf("the main checkout moved to %q, want %q", branch, a.BaseRef)
	}

	// Asking again returns the same lead and makes nothing new.
	again, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	// Stored times have second precision, so compare what the store kept.
	if again.Worktree != a.Worktree || again.CreatedAt.Unix() != a.CreatedAt.Unix() {
		t.Errorf("EnsureLead() made a second lead: %+v", again)
	}
	agents, _ := f.st.Agents(ctx, "hello-stack")
	if len(agents) != 1 {
		t.Errorf("agents = %d, want 1 (the lead)", len(agents))
	}
}

// The lead's safety is the settings file, not the brief: Claude Code enforces
// deny rules, the model doesn't.
// The lead has the user's machine, like the user's own Claude Code in a
// terminal: every tool, no fence on reads, and a mode that asks first (D89).
// A lead made when it had none of that loses the old deny list.
func TestLeadHasTheUsersMachine(t *testing.T) {
	f := leadFixture(t)
	home := f.m.Paths.LeadHome("hello-stack")
	old := `{"permissions":{"defaultMode":"default","deny":["Bash","Write"],"blockReadsOutsideWorkingDirectories":true}}`
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.EnsureLead(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions map[string]any `json:"permissions"`
		Sandbox     any            `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions["defaultMode"] != "default" {
		t.Errorf("defaultMode = %v, want default: the lead asks before it acts", settings.Permissions["defaultMode"])
	}
	for _, key := range []string{"deny", "blockReadsOutsideWorkingDirectories"} {
		if _, ok := settings.Permissions[key]; ok {
			t.Errorf("the lead's permissions still have %q: %s", key, raw)
		}
	}
	if settings.Sandbox != nil {
		t.Errorf("the lead's shell is sandboxed: %s", raw)
	}
	// Its searches go to an Explore on Haiku, not on the lead's own model (D84).
	if explore, err := os.ReadFile(filepath.Join(home, ".claude", "agents", "Explore.md")); err != nil || !strings.Contains(string(explore), "model: haiku") {
		t.Errorf("the lead's Explore = %q, %v", explore, err)
	}

	// Its brief is the lead's, not an agent's, and points at the right worktree.
	text, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"you are hello-stack's lead", "a shell on the user's machine", a.Worktree} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the lead's brief doesn't mention %q", want)
		}
	}
	if strings.Contains(string(text), "agentbox media screenshot") {
		t.Error("the lead got an agent's brief, which tells it to run commands")
	}
}

func TestSyncLeadFollowsTheBranch(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	first := a.BaseCommit

	// The user commits on the branch the lead stands on.
	if err := os.WriteFile(filepath.Join(f.repo.Root, "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo.Root, "add", "-A")
	testutil.Git(t, f.repo.Root, "commit", "-q", "-m", "second")

	a, err = f.m.SyncLead(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if a.BaseCommit == first {
		t.Fatal("SyncLead() stayed on the old commit")
	}
	if _, err := os.Stat(filepath.Join(a.Worktree, "NEW.md")); err != nil {
		t.Errorf("the lead didn't see the new commit's file: %v", err)
	}
	stored, _ := f.st.Agent(ctx, "hello-stack", state.LeadName)
	if stored.BaseCommit != a.BaseCommit {
		t.Errorf("stored base commit = %s, want %s", stored.BaseCommit, a.BaseCommit)
	}
}

// An agent's machine mounts the repository's .git but not the lead's worktree,
// so a `git worktree prune` run there unregisters it: its files and .git
// pointer stay, and every git command in it fails. The next message rebuilds it.
func TestLeadRecoversAWorktreeGitForgot(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := a.Worktree + ".elsewhere"
	if err := os.Rename(a.Worktree, elsewhere); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo.Root, "worktree", "prune")
	if err := os.Rename(elsewhere, a.Worktree); err != nil {
		t.Fatal(err)
	}

	// The user commits, so the next message has to move the lead.
	if err := os.WriteFile(filepath.Join(f.repo.Root, "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo.Root, "add", "-A")
	testutil.Git(t, f.repo.Root, "commit", "-q", "-m", "second")

	a, err = f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	a, err = f.m.SyncLead(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if at := testutil.Git(t, a.Worktree, "rev-parse", "HEAD"); at != a.BaseCommit {
		t.Errorf("worktree at %s, want %s", at, a.BaseCommit)
	}
	if _, err := os.Stat(filepath.Join(a.Worktree, "NEW.md")); err != nil {
		t.Errorf("the lead didn't see the new commit's file: %v", err)
	}
}

// A repository cloned afresh in the project's place knows neither the lead's
// worktree nor, when it stood on a branch never pushed, the commit or branch it
// was made on. The lead commits nothing, so nothing is lost by rebuilding it on
// the branch the project is on now.
func TestLeadRecoversFromACommitThatIsGone(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	testutil.Git(t, f.repo.Root, "checkout", "-q", "-b", "fix/unpushed")
	if err := os.WriteFile(filepath.Join(f.repo.Root, "UNPUSHED.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo.Root, "add", "-A")
	testutil.Git(t, f.repo.Root, "commit", "-q", "-m", "unpushed")
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	gone := a.BaseCommit

	// The user clones main afresh where the project was: no such worktree,
	// branch or commit.
	old := f.repo.Root + ".old"
	if err := os.Rename(f.repo.Root, old); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(old), "clone", "-q", "--no-local", "--single-branch", "--branch", "main", old, f.repo.Root)
	if err := os.RemoveAll(a.Worktree); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ResolveCommit(gone); err == nil {
		t.Fatalf("%s is still in the repository", gone)
	}

	a, err = f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatalf("EnsureLead() = %v, want the lead rebuilt on main", err)
	}
	main := testutil.Git(t, f.repo.Root, "rev-parse", "main")
	if a.BaseRef != "main" || a.BaseCommit != main {
		t.Errorf("lead on %s at %s, want main at %s", a.BaseRef, a.BaseCommit, main)
	}
	if at := testutil.Git(t, a.Worktree, "rev-parse", "HEAD"); at != main {
		t.Errorf("worktree at %s, want %s", at, main)
	}
	stored, _ := f.st.Agent(ctx, "hello-stack", state.LeadName)
	if stored.BaseRef != "main" || stored.BaseCommit != main {
		t.Errorf("stored lead on %s at %s, want main at %s", stored.BaseRef, stored.BaseCommit, main)
	}
}

func TestDestroyLeadLeavesTheProjectAlone(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.m.DestroyLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Worktree); !os.IsNotExist(err) {
		t.Errorf("the lead's worktree is still there (stat: %v)", err)
	}
	if _, err := os.Stat(f.m.Paths.LeadHome("hello-stack")); !os.IsNotExist(err) {
		t.Errorf("the lead's private HOME is still there (stat: %v)", err)
	}
	if _, err := f.m.Lead(ctx, "hello-stack"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Lead() = %v, want ErrNotFound", err)
	}
	// The repository is untouched: the lead never had a branch to delete.
	if branch := testutil.Git(t, f.repo.Root, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Errorf("the main checkout is on %q", branch)
	}
	// Destroying twice is not an error: the chat may never have been used.
	if err := f.m.DestroyLead(ctx, "hello-stack"); err != nil {
		t.Errorf("DestroyLead() again: %v", err)
	}
}

// "lead" is reserved, so an ordinary agent can never take the project chat's place.
func TestLeadNameIsReserved(t *testing.T) {
	f := leadFixture(t)
	_, err := f.m.Create(context.Background(), "hello-stack", agent.CreateOptions{AI: "none", Name: state.LeadName})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("Create(--name lead) = %v, want a reserved-name error", err)
	}
}

// The lead runs with a private HOME, but its AI tool is usually reached through
// a mise shim, and a shim finds itself under HOME. Without mise's own
// directories named outright it fails with "not a valid shim" and the adapter
// dies before saying anything useful. Found on a real machine after 0.3.0.
func TestLeadChatCommandLetsItsToolsFindThemselves(t *testing.T) {
	ctx := context.Background()
	// Pinned rather than read from the ambient environment: toolEnv falls
	// back to $HOME only when no XDG_*_HOME is set, and a login manager can
	// set those from the passwd database's home, which need not agree with
	// an overridden $HOME. Clearing them keeps the two in lockstep here.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, "")
	}
	// toolEnv passes an existing MISE_*_DIR straight through, unlike the
	// XDG_*_HOME vars above, which it only reads with os.Getenv: a merely
	// empty value still counts as "already chosen" (os.Environ() carries
	// "MISE_DATA_DIR=" either way), so these need unsetting outright, not
	// just blanking, or a host that already ran mise leaks its real dirs in.
	for _, name := range []string{"MISE_DATA_DIR", "MISE_CONFIG_DIR", "MISE_CACHE_DIR", "MISE_STATE_DIR"} {
		unsetenv(t, name)
	}
	f := leadFixture(t)
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	env := leadEnv(t, f, a)
	// Its own HOME, which is what the permission rules live in.
	if env["HOME"] != f.m.Paths.LeadHome("hello-stack") {
		t.Errorf("HOME = %q, want the lead's own", env["HOME"])
	}
	// And enough for a tool installed with mise to find itself anyway.
	for _, name := range []string{"MISE_DATA_DIR", "MISE_CONFIG_DIR", "MISE_CACHE_DIR", "MISE_STATE_DIR"} {
		if env[name] == "" {
			t.Errorf("%s isn't set: a mise shim would fail with the lead's HOME", name)
			continue
		}
		if strings.HasPrefix(env[name], env["HOME"]) {
			t.Errorf("%s = %q points into the lead's own HOME, where no tool is installed", name, env[name])
		}
		if !strings.HasPrefix(env[name], home) {
			t.Errorf("%s = %q, want it under the user's home %q", name, env[name], home)
		}
	}
	// The host's own Claude Code state stays out of reach (D6).
	if _, ok := env["CLAUDE_CONFIG_DIR"]; ok {
		t.Error("CLAUDE_CONFIG_DIR leaked into the lead's environment")
	}
}

// TestReconfigureLeadRewritesTheBriefForTheProjectsModel checks that changing
// the model a project's agents are created on reaches the chat that creates
// them — at the moment it changes, not whenever the chat happens to start next
// — and that rewriting the brief doesn't cost the lead the model it is itself
// running on.
func TestReconfigureLeadRewritesTheBriefForTheProjectsModel(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	// The lead has the tools to create agents, as it does under the daemon;
	// nothing in this test reaches the socket, only the brief it implies.
	f.m.LeadSocketPath = func(project string) string { return filepath.Join(t.TempDir(), project+".sock") }
	// A menu a Claude Code chat really advertised, which is the only place the
	// models named in the brief may come from.
	if err := f.st.SetSetting(ctx, state.SettingClaudeModelChoices,
		`[{"value":"default","name":"Default (recommended)"},{"value":"opus","name":"Opus 5"},{"value":"haiku","name":"Haiku 4.5"}]`); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	brief := func() string {
		t.Helper()
		text, err := os.ReadFile(filepath.Join(f.m.Paths.LeadHome(a.Project), ".claude", "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}
	if strings.Contains(brief(), "You choose each agent's model") {
		t.Error("a project that has chosen nothing gets the section about choosing")
	}

	// The lead is running on a model of its own, written into its settings by
	// PrepareChatModel. Rewriting the brief must not take it away.
	if err := f.m.PrepareChatModel(ctx, a, "claude-fable-5-1", state.DefaultClaudeCompactWindow); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetProjectAgentModel(ctx, "hello-stack", state.AgentModelAuto); err != nil {
		t.Fatal(err)
	}
	if err := f.m.ReconfigureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You choose each agent's model", "Never choose Fable", "`opus`, `haiku`"} {
		if !strings.Contains(brief(), want) {
			t.Errorf("after moving the project to auto, the brief doesn't mention %q", want)
		}
	}
	// "default" is the menu's word for the tool's own default, not a model to
	// weigh the cost of.
	if strings.Contains(brief(), "`default`") {
		t.Error("the brief offers `default` as a model to choose")
	}
	settings, err := os.ReadFile(filepath.Join(f.m.Paths.LeadHome(a.Project), ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "claude-fable-5-1") {
		t.Error("rewriting the brief lost the model the lead itself runs on")
	}
	if !strings.Contains(string(settings), `"autoCompactWindow": 200000`) {
		t.Errorf("rewriting the brief lost the context the lead compacts at: %s", settings)
	}

	// And a project naming one model for every agent says so in one line.
	if err := f.st.SetProjectAgentModel(ctx, "hello-stack", "haiku"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.ReconfigureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief(), "New agents start on `haiku` with a 200k context window, the project's choice") {
		t.Error("the brief doesn't say which model this project's agents are created on")
	}
	if strings.Contains(brief(), "You choose each agent's model") {
		t.Error("a project that named a model still asks the chat to choose one")
	}

	// A project nobody has chatted with has no brief to rewrite, which is not
	// an error: EnsureLead renders it from the settings as they are then.
	if err := f.m.ReconfigureLead(ctx, "never-chatted-with"); err != nil {
		t.Errorf("ReconfigureLead on a project with no chat: %v", err)
	}
}

// TestLeadFollowsTheProjectsClaudeAccount: the lead has no account of its own
// to pick, so it runs on the one its project resolves to — not the one it was
// made with. Moving the project moves the lead's row at once (ReconfigureLead,
// which the daemon calls when the project's accounts change) and again
// whenever its chat starts (EnsureLead), and the chat that starts next logs in
// with that account's token. Its conversation, which Claude Code keeps in the
// lead's HOME, is left alone.
func TestLeadFollowsTheProjectsClaudeAccount(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if a.ClaudeAccount != "default" {
		t.Fatalf("a new lead runs on %q, want the machine's default account", a.ClaudeAccount)
	}
	session := filepath.Join(f.m.Paths.LeadHome("hello-stack"), ".claude", "projects", "lead", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(session), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(session, []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		t.Helper()
		a, err := f.m.Lead(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		return a.ClaudeAccount
	}

	// agentbox claude-account hello-stack personal --allow personal
	if err := f.m.Creds.SaveClaudeToken("personal", "personal-token"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetProjectClaudeAccounts(ctx, "hello-stack", "personal", []string{"personal"}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.ReconfigureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if got := stored(); got != "personal" {
		t.Errorf("after the project moved, the lead runs on %q, want %q", got, "personal")
	}

	// A lead whose row still names the old account — moved before this fix, or
	// while the daemon couldn't reach it — is moved when its chat starts.
	if err := f.st.SetAgentClaudeAccount(ctx, "hello-stack", state.LeadName, "default"); err != nil {
		t.Fatal(err)
	}
	a, err = f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if a.ClaudeAccount != "personal" || stored() != "personal" {
		t.Errorf("EnsureLead() runs the lead on %q (stored %q), want %q", a.ClaudeAccount, stored(), "personal")
	}
	tools := filepath.Join(f.m.Paths.Tools(), ".local", "bin")
	adapter := filepath.Join(f.m.Paths.Tools(), "node_modules", ".bin")
	for _, stub := range []string{filepath.Join(tools, "claude"), filepath.Join(adapter, "claude-agent-acp"), f.m.GHPath()} {
		if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd, err := f.m.LeadChatCommand(ctx, a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN=personal-token") {
		t.Error("the lead's chat doesn't start with the project's account's token")
	}
	if _, err := os.Stat(session); err != nil {
		t.Errorf("moving the lead to another account lost its conversation: %v", err)
	}

	// A project that leaves nothing it may use is refused, rather than left
	// running its chat on an account it no longer allows.
	if err := f.st.SetProjectClaudeAccounts(ctx, "hello-stack", "", []string{"personal"}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Creds.SetDefaultClaudeAccount("default"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.EnsureLead(ctx, "hello-stack"); err == nil || !strings.Contains(err.Error(), `not "default"`) {
		t.Errorf("EnsureLead() on a project that doesn't allow the default account: %v", err)
	}
	if err := f.m.ReconfigureLead(ctx, "hello-stack"); err == nil {
		t.Error("ReconfigureLead() says nothing about an account the project doesn't allow")
	}
	if got := stored(); got != "personal" {
		t.Errorf("a refused account moved the lead to %q", got)
	}
}

// leadEnv is the environment LeadChatCommand gives a's chat, with stub tools
// on the PATH so no download is attempted.
func leadEnv(t *testing.T, f fixture, a state.Agent) map[string]string {
	t.Helper()
	stub := []byte("#!/bin/sh\nexit 0\n")
	for _, path := range []string{
		filepath.Join(f.m.Paths.Tools(), ".local", "bin", "claude"),
		filepath.Join(f.m.Paths.Tools(), "node_modules", ".bin", "claude-agent-acp"),
		f.m.GHPath(),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, stub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd, err := f.m.LeadChatCommand(context.Background(), a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range cmd.Env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env[name] = value
		}
	}
	return env
}

// In the VM the lead's remote, git@github.com, needs an ssh key the VM's home
// doesn't have ("Host key verification failed"), and the lead had no token
// either. It now gets the GitHub account its project's agents default to: the
// token in its environment, and a git config in its own HOME that sends
// github.com over HTTPS with that token, with no gh needed.
func TestLeadGetsTheProjectsGitHubAccount(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	// The user's own git config (the fixture points GIT_CONFIG_GLOBAL at a
	// file of its own), which the lead's identity comes from and which must
	// be left as it is.
	userConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	if userConfig == "" {
		t.Fatal("the fixture set no GIT_CONFIG_GLOBAL")
	}
	testutil.Git(t, f.repo.Root, "config", "--global", "user.name", "Ada Lovelace")
	testutil.Git(t, f.repo.Root, "config", "--global", "user.email", "ada@example.com")
	userBefore, err := os.ReadFile(userConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"personal", "work"} {
		if err := f.m.Creds.SaveGitHubToken(account, "gho_"+account); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.m.Creds.SetDefaultGitHubAccount("personal"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Store.SetProjectGitHubAccount(ctx, "hello-stack", "work"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}

	// The project's account wins over the machine's default, as for an agent.
	env := leadEnv(t, f, a)
	if env["GH_TOKEN"] != "gho_work" || env["GITHUB_TOKEN"] != "gho_work" {
		t.Errorf("GH_TOKEN = %q, GITHUB_TOKEN = %q, want the project's account, gho_work", env["GH_TOKEN"], env["GITHUB_TOKEN"])
	}

	home := f.m.Paths.LeadHome("hello-stack")
	config, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err != nil {
		t.Fatalf("the lead has no .gitconfig: %v", err)
	}
	if !strings.Contains(string(config), "Ada Lovelace") {
		t.Errorf(".gitconfig has no identity from the user's own:\n%s", config)
	}
	// Real git, reading the lead's HOME and nothing else, as its chat does.
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = a.Worktree
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GH_TOKEN=" + env["GH_TOKEN"], "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, remote := range []string{"git@github.com:octo/repo.git", "ssh://git@github.com/octo/repo.git"} {
		if got := git("", "ls-remote", "--get-url", remote); got != "https://github.com/octo/repo.git" {
			t.Errorf("%s resolves to %q, want it rewritten to HTTPS", remote, got)
		}
	}
	creds := git("protocol=https\nhost=github.com\n\n", "credential", "fill")
	if !strings.Contains(creds, "username=x-access-token\n") || !strings.Contains(creds, "password=gho_work") {
		t.Errorf("git credential fill answered:\n%s\nwant the project's token", creds)
	}
	if got := git("", "config", "user.email"); got != "ada@example.com" {
		t.Errorf("user.email = %q, want the user's own", got)
	}

	// Once the project has no account left, the token and the config go.
	for _, account := range []string{"personal", "work"} {
		if err := f.m.Creds.RemoveGitHubAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.m.Store.SetProjectGitHubAccount(ctx, "hello-stack", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.m.ReconfigureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lead's .gitconfig is still there with no GitHub account: %v", err)
	}
	env = leadEnv(t, f, a)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if _, ok := env[name]; ok {
			t.Errorf("%s is set with no GitHub account", name)
		}
	}
	if got, _ := os.ReadFile(userConfig); string(got) != string(userBefore) {
		t.Errorf("the user's own .gitconfig changed:\n%s", got)
	}
}

// A project naming an account that is gone leaves its chat without GitHub,
// not without a chat.
func TestLeadStartsWithAGitHubAccountThatIsGone(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	if err := f.m.Store.SetProjectGitHubAccount(ctx, "hello-stack", "gone"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if env := leadEnv(t, f, a); env["GH_TOKEN"] != "" {
		t.Errorf("GH_TOKEN = %q, want none", env["GH_TOKEN"])
	}
	if _, err := os.Stat(filepath.Join(f.m.Paths.LeadHome("hello-stack"), ".gitconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lead has a .gitconfig with no GitHub account: %v", err)
	}
}

// A project's chat is given its project's enabled connectors as MCP servers,
// through the same relay as an agent's, pointed at its own socket; a change
// to them reaches it without touching the rest of Claude Code's state.
func TestLeadIsGivenItsProjectsConnectors(t *testing.T) {
	ctx := context.Background()
	f := leadFixture(t)
	socket := filepath.Join(t.TempDir(), "lead.sock")
	f.m.LeadSocketPath = func(string) string { return socket }
	f.m.Binary = "/usr/local/bin/agentbox"
	for _, c := range []state.Connector{
		{Project: "hello-stack", Name: "notion", URL: "https://mcp.notion.com/mcp", Auth: "oauth", Enabled: true},
		{Project: "hello-stack", Name: "linear", URL: "https://mcp.linear.app/mcp", Auth: "oauth"},
		{Project: "hello-stack", Agent: "agent-01", Name: "sentry", URL: "https://mcp.sentry.dev/mcp", Auth: "none", Enabled: true},
	} {
		if err := f.st.SetConnector(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.m.EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.m.Paths.LeadHome("hello-stack"), ".claude.json")
	type server struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	servers := func() map[string]server {
		t.Helper()
		var claude struct {
			MCPServers map[string]server `json:"mcpServers"`
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &claude); err != nil {
			t.Fatal(err)
		}
		return claude.MCPServers
	}
	got := servers()
	if s := got["agentbox"]; s.Env["AGENTBOX_SOCKET"] != socket || strings.Join(s.Args, " ") != "mcp" {
		t.Errorf("the lead's own server = %+v", s)
	}
	if s := got["notion"]; s.Command != f.m.Binary || strings.Join(s.Args, " ") != "connector mcp notion" || s.Env["AGENTBOX_IN_AGENT_SOCKET"] != socket {
		t.Errorf("notion = %+v, want the relay on the lead's socket", s)
	}
	if _, ok := got["linear"]; ok {
		t.Error("the lead was given a connector that is turned off")
	}
	if _, ok := got["sentry"]; ok {
		t.Error("the lead was given an agent's own connector")
	}

	// Claude Code writes its own state here too, which a change leaves alone.
	b, _ := os.ReadFile(path)
	var state0 map[string]json.RawMessage
	_ = json.Unmarshal(b, &state0)
	state0["numStartups"] = json.RawMessage(`3`)
	b, _ = json.Marshal(state0)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetConnector(ctx, state.Connector{Project: "hello-stack", Name: "linear", URL: "https://mcp.linear.app/mcp", Auth: "oauth", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncConnectors(ctx, "hello-stack", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers()["linear"]; !ok {
		t.Error("linear, turned on, didn't reach the lead")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"numStartups":3`) || !strings.Contains(string(b), "hasCompletedOnboarding") {
		t.Errorf("the rest of ~/.claude.json was lost:\n%s", b)
	}
}
