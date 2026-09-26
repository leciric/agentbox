package agent_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// claudeAgentFixture is destroyFixture's agent-01, but running Claude Code, so
// it has an account to change.
func claudeAgentFixture(t *testing.T, f fixture) state.Agent {
	t.Helper()
	worktree := f.m.Paths.Worktree("hello-stack", "agent-01")
	if err := f.repo.AddWorktree(worktree, "agentbox/agent-01", "HEAD"); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01",
		Branch: "agentbox/agent-01", Worktree: worktree, AI: "claude",
		CreatedAt: time.Now(), Status: state.AgentReady,
	}
	if err := f.st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestGetParsesTheRefAndLooksUpTheAgent checks the two ways Get can fail: a
// ref that isn't project/agent, and one that parses but names nothing stored.
func TestGetParsesTheRefAndLooksUpTheAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := destroyFixture(t, f)
	ctx := context.Background()

	got, err := f.m.Get(ctx, "hello-stack/agent-01")
	if err != nil || got.Name != a.Name || got.Project != a.Project {
		t.Fatalf("Get() = %+v, %v, want %+v", got, err, a)
	}
	if _, err := f.m.Get(ctx, "not-a-ref"); err == nil {
		t.Error("Get() with an unparseable ref should fail")
	}
	if _, err := f.m.Get(ctx, "hello-stack/missing"); err == nil {
		t.Error("Get() of an agent that was never stored should fail")
	}
}

func TestEnvPathIsUnderTheAgentUsersConfig(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	if got, want := f.m.EnvPath(), "/home/dev/.config/agentbox/env"; got != want {
		t.Errorf("EnvPath() = %q, want %q", got, want)
	}
}

// TestShellCommandQuotesTheWorktree checks the incus command line that
// attaches a terminal: it runs as the agent's own user, in a tmux session
// named after the worktree, whose path is quoted so a space or an apostrophe
// in it can't break out of the shell -c argument.
func TestShellCommandQuotesTheWorktree(t *testing.T) {
	got := agent.ShellCommand("/usr/bin/incus", "ab-hello-agent-01", "dev", "/work/it's mine")
	want := []string{"/usr/bin/incus", "exec", "ab-hello-agent-01", "-t", "--", "runuser", "-l", "dev", "-c",
		`tmux new-session -A -s main -c '/work/it'\''s mine'`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ShellCommand() = %#v, want %#v", got, want)
	}
}

func TestShellArgsUsesTheManagersIncusAndUser(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := state.Agent{Instance: "ab-hello-stack-agent-01", Worktree: "/work/agent-01"}
	got := f.m.ShellArgs(a)
	if got[0] != f.m.Incus.Path() || got[2] != "ab-hello-stack-agent-01" {
		t.Errorf("ShellArgs() = %v, want it to use the manager's incus and the agent's instance", got)
	}
	if !strings.Contains(strings.Join(got, " "), "runuser -l dev -c") {
		t.Errorf("ShellArgs() = %v, want it to run as the agent's user dev", got)
	}
}

func TestExecCommandRunsInTheWorktree(t *testing.T) {
	got := agent.ExecCommand("/work/it's mine", "npm test")
	if got != `cd '/work/it'\''s mine' && npm test` {
		t.Errorf("ExecCommand() = %q", got)
	}
}

// TestExecRunsInTheWorktreeAsTheHostUser checks that Exec refuses a stopped
// agent, and otherwise runs the command through the same runuser login shell
// as a real terminal, capturing its output.
func TestExecRunsInTheWorktreeAsTheHostUser(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo ran ;;
esac`))
	a := destroyFixture(t, f)
	var out bytes.Buffer
	if err := f.m.Exec(context.Background(), a, "true", nil, &out, &out); err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "ran" {
		t.Errorf("Exec() wrote %q, want the fake incus's output", got)
	}
}

// TestRequireRunningNamesWhatToDoNext checks the actionable message every
// caller of a stopped or paused agent gets, through Exec and PrepareShell,
// which both refuse before touching the machine.
func TestRequireRunningNamesWhatToDoNext(t *testing.T) {
	for _, tc := range []struct{ status, want string }{
		{"Frozen", "is paused: run agentbox resume hello-stack/agent-01"},
		{"Stopped", "is stopped: run agentbox start hello-stack/agent-01"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f := setup(t, fakeIncus(t, `case "$1" in list) echo '[{"name":"ab-hello-stack-agent-01","status":"`+tc.status+`"}]' ;; esac`))
			a := destroyFixture(t, f)
			if err := f.m.PrepareShell(context.Background(), a); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("PrepareShell() on a %s agent = %v, want it to mention %q", tc.status, err, tc.want)
			}
			if err := f.m.Exec(context.Background(), a, "true", nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Exec() on a %s agent = %v, want it to mention %q", tc.status, err, tc.want)
			}
		})
	}
	f := setup(t, fakeIncus(t, `case "$1" in list) echo '[]' ;; esac`))
	a := destroyFixture(t, f)
	if err := f.m.PrepareShell(context.Background(), a); err == nil {
		t.Error("PrepareShell() with no instance at all should fail")
	}
}

// PrepareShell also proves a running agent gets its tmux session prepared,
// not just refused.
func TestPrepareShellStartsTheTmuxSessionOfARunningAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) exit 0 ;;
esac`))
	a := destroyFixture(t, f)
	if err := f.m.PrepareShell(context.Background(), a); err != nil {
		t.Errorf("PrepareShell() on a running agent = %v", err)
	}
}

func TestStopFallsBackToForceWhenGracefulStopFails(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  stop)
    if [ "$3" = "--force" ]; then exit 0; fi
    echo "Error: won't stop gracefully" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)
	if err := f.m.Stop(context.Background(), a); err != nil {
		t.Errorf("Stop() = %v, want the forced stop to make it succeed", err)
	}
}

func TestStopSucceedsGracefully(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  stop)
    [ "$3" = "--force" ] && { echo "should not need --force" >&2; exit 1; }
    exit 0 ;;
esac`))
	a := destroyFixture(t, f)
	if err := f.m.Stop(context.Background(), a); err != nil {
		t.Errorf("Stop() = %v", err)
	}
}

func TestPauseAndResume(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1 $2" in
  "pause ab-hello-stack-agent-01") exit 0 ;;
  "resume ab-hello-stack-agent-01") exit 0 ;;
esac
echo "unexpected: $*" >&2
exit 99`))
	a := destroyFixture(t, f)
	if err := f.m.Pause(context.Background(), a); err != nil {
		t.Errorf("Pause() = %v", err)
	}
	if err := f.m.Resume(context.Background(), a); err != nil {
		t.Errorf("Resume() = %v", err)
	}
}

// TestDestroyHandsBackFilesWhenTheMachineIsRunning is the one destroy_test.go
// case where the instance is up, so Destroy chowns the worktree back to the
// host user before deleting it.
func TestDestroyHandsBackFilesWhenTheMachineIsRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "handback")
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo "$@" >>`+marker+` ;;
  delete) exit 0 ;;
esac`))
	a := destroyFixture(t, f)
	if err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true}); err != nil {
		t.Fatalf("Destroy() = %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "chown") || !strings.Contains(string(got), a.Worktree) {
		t.Errorf("Destroy() on a running agent didn't hand its files back: %q", got)
	}
}

func TestListReportsEachAgentsLiveState(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
esac`))
	ctx := context.Background()
	running := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	missing := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	creating := state.Agent{Project: "hello-stack", Name: "agent-03", Instance: "ab-hello-stack-agent-03", Worktree: t.TempDir(), Status: state.AgentCreating, CreatedAt: time.Now()}
	leadWorktree := t.TempDir()
	lead := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead, Worktree: leadWorktree, Status: state.AgentReady, CreatedAt: time.Now()}
	for _, a := range []state.Agent{running, missing, creating, lead} {
		if err := f.st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	statuses, err := f.m.List(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range statuses {
		got[s.Name] = s.State
	}
	want := map[string]string{"agent-01": "running", "agent-02": "missing", "agent-03": "incomplete", state.LeadName: "host"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List() states = %+v, want %+v", got, want)
	}
}

func TestListOfAProjectWithNoAgentsIsEmpty(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	statuses, err := f.m.List(context.Background(), "hello-stack")
	if err != nil || len(statuses) != 0 {
		t.Errorf("List() = %+v, %v, want none", statuses, err)
	}
}

// TestDiffShowsWhatTheAgentChangedSinceItWasCreated checks Diff against the
// commit the agent was made from, on a real worktree.
func TestDiffShowsWhatTheAgentChangedSinceItWasCreated(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := destroyFixture(t, f)
	base := testutil.Git(t, a.Worktree, "rev-parse", "HEAD")
	if err := f.st.SetAgentBaseCommit(context.Background(), a.Project, a.Name, base); err != nil {
		t.Fatal(err)
	}
	a.BaseCommit = base
	if err := os.WriteFile(filepath.Join(a.Worktree, "work.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, a.Worktree, "add", "work.txt")

	diff, err := f.m.Diff(a, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "work.txt") {
		t.Errorf("Diff() = %q, want it to mention work.txt", diff)
	}
	stat, err := f.m.Diff(a, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stat, "1 file changed") && !strings.Contains(stat, "work.txt") {
		t.Errorf("Diff(stat) = %q", stat)
	}
}

// TestRewriteAgentEnvReportsWhichAgentsItCouldntReach checks that a rewrite
// carries on past a failing agent and names only the ones it couldn't reach,
// leaving the others untouched.
func TestRewriteAgentEnvReportsWhichAgentsItCouldntReach(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"},{"name":"ab-hello-stack-agent-02","status":"Running"}]' ;;
  exec)
    if [ "$2" = "ab-hello-stack-agent-02" ]; then echo "Error: boom" >&2; exit 1; fi
    cat >/dev/null ;;
esac`))
	ctx := context.Background()
	destroyFixture(t, f)
	a2 := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := f.st.AddAgent(ctx, a2); err != nil {
		t.Fatal(err)
	}
	err := f.m.RewriteAgentEnv(ctx)
	if err == nil || !strings.Contains(err.Error(), "hello-stack/agent-02") || strings.Contains(err.Error(), "agent-01") {
		t.Errorf("RewriteAgentEnv() = %v, want it to name only the failing agent-02", err)
	}
}

// TestRewriteAgentEnvSkipsAgentsThatArentRunning proves a stopped or
// not-ready agent is left alone rather than failed.
func TestRewriteAgentEnvSkipsAgentsThatArentRunning(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
  exec) echo "should not run against a stopped agent" >&2; exit 1 ;;
esac`))
	ctx := context.Background()
	destroyFixture(t, f)
	if err := f.m.RewriteAgentEnv(ctx); err != nil {
		t.Errorf("RewriteAgentEnv() = %v, want a stopped agent skipped rather than failed", err)
	}
}

// TestRewriteBriefsReportsWhichAgentsItCouldntReach mirrors the env rewrite:
// one agent's brief fails to write, and only that one is named.
func TestRewriteBriefsReportsWhichAgentsItCouldntReach(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"},{"name":"ab-hello-stack-agent-02","status":"Running"}]' ;;
  exec)
    if [ "$2" = "ab-hello-stack-agent-02" ]; then echo "Error: boom" >&2; exit 1; fi
    cat >/dev/null ;;
esac`))
	ctx := context.Background()
	destroyFixture(t, f)
	a2 := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := f.st.AddAgent(ctx, a2); err != nil {
		t.Fatal(err)
	}
	err := f.m.RewriteBriefs(ctx, "hello-stack")
	if err == nil || !strings.Contains(err.Error(), "hello-stack/agent-02") || strings.Contains(err.Error(), "agent-01") {
		t.Errorf("RewriteBriefs() = %v, want it to name only the failing agent-02", err)
	}
}

func TestSetClaudeAccountRefusesAgentsThatDontRunClaudeCode(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := destroyFixture(t, f) // AI is "" here, not claude
	if _, err := f.m.SetClaudeAccount(context.Background(), a, "work"); err == nil || !strings.Contains(err.Error(), "there is no account to change") {
		t.Errorf("SetClaudeAccount() on a non-Claude agent = %v", err)
	}
}

// TestSetClaudeAccountMovesARunningAgent checks the whole path: the account
// has to be a real, stored one, the machine has to be up to receive the new
// token, and both the returned agent and the stored row end up on it.
func TestSetClaudeAccountMovesARunningAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) cat >/dev/null ;;
esac`))
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	a := claudeAgentFixture(t, f)

	got, err := f.m.SetClaudeAccount(ctx, a, "work")
	if err != nil {
		t.Fatalf("SetClaudeAccount() = %v", err)
	}
	if got.ClaudeAccount != "work" {
		t.Errorf("SetClaudeAccount() returned %+v, want ClaudeAccount work", got)
	}
	stored, err := f.st.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ClaudeAccount != "work" {
		t.Errorf("stored agent's ClaudeAccount = %q, want work", stored.ClaudeAccount)
	}
}

// TestSetClaudeAccountOnAStoppedAgentStillRecordsIt checks that a stopped or
// paused agent can have its stored account changed without its machine being
// up: the token is a file inside it, so it can't be written now, but nothing
// stops the change itself, and Start writes the new token in when the agent
// next comes up. exec failing loudly if it ran at all confirms nothing tried
// to reach the (stopped) machine.
func TestSetClaudeAccountOnAStoppedAgentStillRecordsIt(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  exec) echo "exec shouldn't run on a stopped agent" >&2; exit 1 ;;
esac`))
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	a := claudeAgentFixture(t, f)
	got, err := f.m.SetClaudeAccount(ctx, a, "work")
	if err != nil || got.ClaudeAccount != "work" {
		t.Fatalf("SetClaudeAccount(work) = %+v, %v", got, err)
	}
	stored, err := f.st.Agent(ctx, a.Project, a.Name)
	if err != nil || stored.ClaudeAccount != "work" {
		t.Fatalf("stored account = %+v, %v", stored, err)
	}
}

// TestStartWritesTheEnvFileAfresh checks that starting an agent writes its env
// file again, so an account changed while it was stopped or paused — which
// SetClaudeAccount/SetGitHubAccount can only record then, not deliver — still
// reaches it, the same way a stopped agent catches up on its secrets.
func TestStartWritesTheEnvFileAfresh(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("personal", "sk-ant-oat01-personal"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ClaudeAccount: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	const path = "/home/dev/.config/agentbox/env"
	if err := os.Remove(filepath.Join(files, a.Instance, path)); err != nil { // as if the machine had been stopped since
		t.Fatal(err)
	}
	// As if the account had moved while the machine was down: the store
	// changes, but there's no file to write it into yet.
	if err := f.st.SetAgentClaudeAccount(ctx, a.Project, a.Name, "work"); err != nil {
		t.Fatal(err)
	}
	a.ClaudeAccount = "work"

	if _, err := f.m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := inAgent(t, files, a.Instance, path); !strings.Contains(got, "sk-ant-oat01-work") {
		t.Errorf("starting the agent didn't write the account it now has:\n%s", got)
	}
}

// TestMoveAgentsClaudeAccountMovesOnlyWhatsOnTheOldAccount checks the helper a
// project-wide account change uses to catch up its existing agents: only
// agents still on the account being replaced move, and an agent already
// pointed elsewhere (its own explicit choice) is left alone.
func TestMoveAgentsClaudeAccountMovesOnlyWhatsOnTheOldAccount(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"},{"name":"ab-hello-stack-agent-02","status":"Stopped"}]' ;;
  exec) echo "exec shouldn't run on a stopped agent" >&2; exit 1 ;;
esac`))
	ctx := context.Background()
	for _, account := range []string{"personal", "work"} {
		if err := f.m.Creds.SaveClaudeToken(account, "sk-ant-oat01-"+account); err != nil {
			t.Fatal(err)
		}
	}
	a1 := claudeAgentFixture(t, f)
	if err := f.st.SetAgentClaudeAccount(ctx, a1.Project, a1.Name, "personal"); err != nil {
		t.Fatal(err)
	}
	a2 := a1
	a2.Name, a2.Instance = "agent-02", "ab-hello-stack-agent-02"
	if err := f.st.AddAgent(ctx, a2); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetAgentClaudeAccount(ctx, a2.Project, a2.Name, "work"); err != nil { // its own choice
		t.Fatal(err)
	}

	if err := f.m.MoveAgentsClaudeAccount(ctx, "hello-stack", "personal", "work"); err != nil {
		t.Fatal(err)
	}
	moved, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil || moved.ClaudeAccount != "work" {
		t.Errorf("agent-01 = %+v, %v, want work", moved, err)
	}
	unchanged, err := f.st.Agent(ctx, "hello-stack", "agent-02")
	if err != nil || unchanged.ClaudeAccount != "work" {
		t.Errorf("agent-02 = %+v, %v, want work (unchanged)", unchanged, err)
	}
}

// TestSetGitHubAccountMovesARunningAgent checks that any agent, not just
// Claude Code ones, can have its GitHub account changed: the returned agent
// and the stored row both end up on the new account.
func TestSetGitHubAccountMovesARunningAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) cat >/dev/null ;;
esac`))
	ctx := context.Background()
	for _, account := range []string{"personal", "work"} {
		if err := f.m.Creds.SaveGitHubToken(account, "gho_"+account); err != nil {
			t.Fatal(err)
		}
	}
	a := destroyFixture(t, f)

	got, err := f.m.SetGitHubAccount(ctx, a, "work")
	if err != nil || got.GitHubAccount != "work" {
		t.Fatalf("SetGitHubAccount(work) = %+v, %v", got, err)
	}
	stored, err := f.st.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GitHubAccount != "work" {
		t.Errorf("stored agent's GitHubAccount = %q, want work", stored.GitHubAccount)
	}
	// An unknown account is refused before anything is written.
	if _, err := f.m.SetGitHubAccount(ctx, got, "gone"); err == nil || !strings.Contains(err.Error(), `no GitHub account named "gone"`) {
		t.Errorf("SetGitHubAccount(gone) = %v, want it refused", err)
	}
}
