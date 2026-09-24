package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

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
	f := leadFixture(t)
	a, err := f.m.EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	// A stub adapter on the PATH, so no download is attempted.
	tools := filepath.Join(f.m.Paths.Tools(), ".local", "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	adapter := filepath.Join(f.m.Paths.Tools(), "node_modules", ".bin")
	if err := os.MkdirAll(adapter, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapter, "claude-agent-acp"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd, err := f.m.LeadChatCommand(ctx, a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range cmd.Env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env[name] = value
		}
	}
	// Its own HOME, which is what the permission rules live in.
	if env["HOME"] != f.m.Paths.LeadHome("hello-stack") {
		t.Errorf("HOME = %q, want the lead's own", env["HOME"])
	}
	// And enough for a tool installed with mise to find itself anyway.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to derive the tool directories from")
	}
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
	if !strings.Contains(brief(), "runs on `haiku`") {
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
	for _, stub := range []string{filepath.Join(tools, "claude"), filepath.Join(adapter, "claude-agent-acp")} {
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
