package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/cli"
	"agentbox/internal/daemon"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/testutil"
)

// unreachableAPI stands in for Anthropic in tests: a port nothing listens on,
// so a token check fails at once and answers "not checked".
const unreachableAPI = "http://127.0.0.1:1"

// isolate points HOME and the XDG directories at a temporary directory.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("AGENTBOX_SOCKET", "")
	t.Setenv("AGENTBOX_NO_AUTOSTART", "1")
	// Nothing here asks Anthropic whether a made-up token is real. A test that
	// wants an answer points this at a server of its own.
	t.Setenv("ANTHROPIC_BASE_URL", unreachableAPI)
	return home
}

// startDaemon runs a daemon in the test process for the isolated state, with a
// stand-in incus that knows no instances.
func startDaemon(t *testing.T) {
	t.Helper()
	p, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "incus")
	script := "#!/bin/sh\ncase \"$1\" in list|query) echo '[]' ;; esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	srv, err := daemon.New(daemon.Config{Paths: p, Incus: incus.Client{Bin: fake}, User: image.User{Name: "dev", UID: 1000, GID: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	c := api.NewClient(p.Socket())
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if c.Ping(ctx) == nil {
			return
		}
	}
	t.Fatal("the daemon didn't start")
}

func run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := cli.NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func mustContain(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}

func TestProjectLifecycle(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "add", repo)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Added project hello-stack", repo, "main (new agents start here)", ".env (copied into new agents)")

	if _, err := run(t, "", "add", filepath.Join(repo, ".")); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("adding the same repo twice: got %v, want an already-exists error", err)
	}

	out, err = run(t, "", "projects")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack", repo)

	out, err = run(t, "", "brief", "hello-stack", "--agent", "agent-07")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Agent: agent-07", "`agentbox/agent-07` (created from `main`)", "- `.env`", "worktrees/hello-stack/agent-07")

	out, err = run(t, "", "remove", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Removed project hello-stack")

	out, _ = run(t, "", "projects")
	mustContain(t, out, "No projects yet")
}

// A project's accounts can be chosen as it is added, so a machine with two
// GitHub logins doesn't have to add the project and then fix it.
func TestAddPicksTheProjectsAccounts(t *testing.T) {
	home := isolate(t)
	startDaemon(t)
	dir := filepath.Join(home, ".config", "agentbox", "credentials", "github")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work.token"), []byte("gho_work\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "", "add", testutil.FixtureRepo(t, "hello-stack"), "--name", "nope", "--github-account", "missing"); err == nil ||
		!strings.Contains(err.Error(), `no GitHub account named "missing"`) {
		t.Errorf("add --github-account missing = %v, want it refused", err)
	}

	out, err := run(t, "", "add", testutil.FixtureRepo(t, "hello-stack"), "--name", "pawly", "--github-account", "work")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "github account", "work")
	out, _ = run(t, "", "projects")
	if !regexp.MustCompile(`pawly\s+-\s+work`).MatchString(out) {
		t.Errorf("projects = %s, want pawly on the account work", out)
	}
}

// The notes a project keeps for its agents: shown, replaced from stdin, added
// to, and folded into the brief every agent is given.
func TestNotesCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "notes", "show", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "no notes yet", "agentbox notes edit hello-stack")

	out, err = run(t, "The app is a pnpm monorepo.\n", "notes", "edit", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Saved hello-stack's notes")

	if out, err = run(t, "", "notes", "append", "hello-stack", "The e2e tests need a Postgres on 5432."); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Saved hello-stack's notes (3 lines)")

	if out, err = run(t, "", "notes", "show", "hello-stack"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "The app is a pnpm monorepo.", "The e2e tests need a Postgres on 5432.")

	// Every agent of the project is told the same, in its brief.
	if out, err = run(t, "", "brief", "hello-stack"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "## Project notes", "The app is a pnpm monorepo.", "The e2e tests need a Postgres on 5432.")

	// Nothing on stdin doesn't quietly empty them.
	if _, err = run(t, "", "notes", "edit", "hello-stack"); err == nil || !strings.Contains(err.Error(), "nothing came in on stdin") {
		t.Errorf("notes edit with no input: got %v, want it refused", err)
	}
}

func TestMediaRetentionCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "media", "retention", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: 30 day(s)") // the default

	out, err = run(t, "", "media", "retention", "hello-stack", "5")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: 5 day(s)")

	out, err = run(t, "", "media", "retention", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: 5 day(s)") // persisted

	if _, err := run(t, "", "media", "retention", "hello-stack", "0"); err == nil {
		t.Error("setting retention to 0 days succeeded, want a positive-integer error")
	}
	if _, err := run(t, "", "media", "retention", "no-such-project"); err == nil {
		t.Error("retention for an unknown project succeeded")
	}
}

func TestFinishNoticesCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "finish-notices", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: lead") // the default

	out, err = run(t, "", "finish-notices", "hello-stack", "off")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: off", "no chat turn and no tokens")

	out, err = run(t, "", "finish-notices", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: off") // persisted

	out, err = run(t, "", "finish-notices", "hello-stack", "chat")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: chat")

	if _, err := run(t, "", "finish-notices", "hello-stack", "quietly"); err == nil {
		t.Error("an unknown finish-notices value was accepted")
	}
	if _, err := run(t, "", "finish-notices", "no-such-project"); err == nil {
		t.Error("finish notices for an unknown project succeeded")
	}
}

func TestProjectModelCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "project", "model", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: default") // follows the installation's setting

	out, err = run(t, "", "project", "model", "hello-stack", "haiku")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: haiku")

	out, err = run(t, "", "project", "model", "hello-stack", "auto")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "its chat chooses a model for each agent")

	out, err = run(t, "", "project", "model", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: auto") // persisted

	// "default" on the command line is how an empty value is typed: it puts
	// the project back on the model new agents start on.
	out, err = run(t, "", "project", "model", "hello-stack", "default")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "the model new agents start on")

	if _, err := run(t, "", "project", "model", "no-such-project"); err == nil {
		t.Error("the model of an unknown project succeeded")
	}
}

func TestAddRejectsRepoWithoutCommits(t *testing.T) {
	isolate(t)
	startDaemon(t)
	testutil.GitEnv(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	if _, err := run(t, "", "add", dir); err == nil || !strings.Contains(err.Error(), "no commits yet") {
		t.Errorf("got %v, want a 'no commits yet' error", err)
	}
}

func TestAddDerivesNameFromDirectory(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	renamed := filepath.Join(filepath.Dir(repo), "My_App.v2")
	if err := os.Rename(repo, renamed); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "add", renamed)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Added project my-app-v2")

	if _, err := run(t, "", "add", "--name", "Bad Name", testutil.FixtureRepo(t, "hello-stack")); err == nil {
		t.Error("add --name 'Bad Name' succeeded")
	}
}

func TestDaemonNotRunning(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "projects"); err == nil || !strings.Contains(err.Error(), "daemon isn't running") {
		t.Errorf("without a daemon and with autostart off: got %v", err)
	}
}

func TestJobsAndSnapshotArgs(t *testing.T) {
	isolate(t)
	startDaemon(t)
	out, err := run(t, "", "jobs")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No jobs yet.")

	for _, args := range [][]string{
		{"snapshot"},
		{"snapshot", "rm", "pawly/agent-01"},
		{"restore", "pawly/agent-01"},
		{"fork"},
		{"jobs", "cancel"},
	} {
		if _, err := run(t, "", args...); err == nil || !strings.Contains(err.Error(), "arg") {
			t.Errorf("agentbox %s: got %v, want an argument error", strings.Join(args, " "), err)
		}
	}
	if _, err := run(t, "", "snapshot", "rm", "pawly/agent-01", "before"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("snapshot rm on an unknown agent: got %v, want not found", err)
	}
}

func TestExecNeedsCommand(t *testing.T) {
	isolate(t)
	_, err := run(t, "", "exec", "pawly/agent-01", "--")
	if err == nil || !strings.Contains(err.Error(), "missing command") {
		t.Errorf("exec without a command: got %v, want a missing-command error", err)
	}
}

func TestWhoamiOutsideAnAgent(t *testing.T) {
	isolate(t)
	// A path that is guaranteed not to exist, regardless of whether this
	// test happens to run inside a real AgentBox agent.
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "agentbox.sock"))
	if _, err := run(t, "", "whoami"); err == nil || !strings.Contains(err.Error(), "not inside an AgentBox agent") {
		t.Errorf("whoami on the host: got %v", err)
	}
}

func TestAuthClaudeTokenFromStdin(t *testing.T) {
	home := isolate(t)

	out, err := run(t, "", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Claude Code   not configured", "Codex         not configured")

	if _, err := run(t, "sk-ant-oat01-example\n", "auth", "claude", "--token-stdin"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "agentbox", "credentials", "claude", "default.token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}
	if b, _ := os.ReadFile(path); string(b) != "sk-ant-oat01-example\n" {
		t.Errorf("token file = %q", b)
	}

	out, _ = run(t, "", "auth", "status")
	mustContain(t, out, "Claude Code   1 account(s): default (default)")

	if _, err := run(t, "two words", "auth", "claude", "--token-stdin"); err == nil {
		t.Error("a token with spaces was accepted")
	}
}

func TestAuthClaudeAccounts(t *testing.T) {
	home := isolate(t)

	out, err := run(t, "", "auth", "claude", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No Claude Code accounts yet")

	for _, account := range []string{"personal", "work"} {
		if _, err := run(t, "sk-ant-oat01-"+account+"\n", "auth", "claude", "--token-stdin", "--account", account); err != nil {
			t.Fatal(err)
		}
	}
	// The first account saved is the default, and each has its own token file.
	out, _ = run(t, "", "auth", "claude", "list")
	mustContain(t, out, "personal", "work", "yes")
	for _, account := range []string{"personal", "work"} {
		b, err := os.ReadFile(filepath.Join(home, ".config", "agentbox", "credentials", "claude", account+".token"))
		if err != nil || string(b) != "sk-ant-oat01-"+account+"\n" {
			t.Errorf("%s token = %q, %v", account, b, err)
		}
	}

	if _, err := run(t, "", "auth", "claude", "default", "work"); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, "", "auth", "status")
	mustContain(t, out, "2 account(s): personal, work (default)")

	if _, err := run(t, "", "auth", "claude", "default", "nope"); err == nil {
		t.Error("an unknown account was accepted as the default")
	}

	out, err = run(t, "", "auth", "claude", "remove", "work")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `Removed the Claude Code account "work"`, `now use "personal"`)
	out, _ = run(t, "", "auth", "claude", "list")
	if strings.Contains(out, "work") {
		t.Errorf("removed account still listed:\n%s", out)
	}
}

// TestAuthGitHubAccounts mirrors TestAuthClaudeAccounts. Accounts are seeded
// by writing tokens directly, since `auth github` checks a token against
// GitHub over the network before storing it, which this test doesn't want to need.
func TestAuthGitHubAccounts(t *testing.T) {
	home := isolate(t)
	startDaemon(t)

	out, err := run(t, "", "auth", "github", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No GitHub accounts yet")

	dir := filepath.Join(home, ".config", "agentbox", "credentials", "github")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"personal", "work"} {
		if err := os.WriteFile(filepath.Join(dir, account+".token"), []byte("gho_"+account+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The first account in name order is the default until another is picked.
	out, _ = run(t, "", "auth", "github", "list")
	mustContain(t, out, "personal", "work", "yes")

	if _, err := run(t, "", "auth", "github", "default", "work"); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, "", "auth", "status")
	mustContain(t, out, "GitHub        2 account(s): personal, work (default)")

	if _, err := run(t, "", "auth", "github", "default", "nope"); err == nil {
		t.Error("an unknown account was accepted as the default")
	}

	out, err = run(t, "", "auth", "github", "remove", "work")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `Removed the GitHub account "work"`, `now use "personal"`)
	out, _ = run(t, "", "auth", "github", "list")
	if strings.Contains(out, "work") {
		t.Errorf("removed account still listed:\n%s", out)
	}
}

// An AgentBox set up before named accounts keeps its token: it becomes "default".
func TestAuthClaudeMigratesTheOldTokenFile(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(home, ".config", "agentbox", "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude-oauth-token"), []byte("sk-ant-oat01-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "claude", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "default", "yes")

	b, err := os.ReadFile(filepath.Join(dir, "claude", "default.token"))
	if err != nil || string(b) != "sk-ant-oat01-old\n" {
		t.Errorf("migrated token = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-oauth-token")); !os.IsNotExist(err) {
		t.Errorf("the old token file is still there: %v", err)
	}
}

// The single shared token used before named accounts becomes "default".
func TestAuthGitHubMigratesTheOldTokenFile(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(home, ".config", "agentbox", "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "github.token"), []byte("gho_old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "github", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "default", "yes")

	b, err := os.ReadFile(filepath.Join(dir, "github", "default.token"))
	if err != nil || string(b) != "gho_old\n" {
		t.Errorf("migrated token = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "github.token")); !os.IsNotExist(err) {
		t.Errorf("the old token file is still there: %v", err)
	}
}

func TestMediaDeleteCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "add", testutil.FixtureRepo(t, "hello-stack")); err != nil {
		t.Fatal(err)
	}

	// It asks first, and takes anything but yes for no.
	out, err := run(t, "n\n", "media", "delete", "hello-stack", "some-id")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Delete 1 item(s)? [y/N]", "Nothing deleted")

	// An ID that isn't there any more is the end this asks for, not an error.
	out, err = run(t, "y\n", "media", "delete", "hello-stack", "some-id")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Deleted 0 item(s)")

	out, err = run(t, "", "media", "delete", "hello-stack", "--all", "--kind", "screenshot", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No media matches")

	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"neither items nor all", "or pass --all", []string{"media", "delete", "hello-stack"}},
		{"both", "not both", []string{"media", "delete", "hello-stack", "--all", "some-id"}},
		{"kind without all", "--kind narrows --all", []string{"media", "delete", "hello-stack", "some-id", "--kind", "log"}},
		{"two agents", "--agent says agent-02", []string{"media", "delete", "hello-stack/agent-01", "--all", "--agent", "agent-02"}},
		{"not a project", "is neither a project nor an agent", []string{"media", "delete", "Not A Project", "--all"}},
	} {
		if _, err := run(t, "", c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error about %q", c.name, err, c.want)
		}
	}
}

// fakeAnthropic answers the token check the way Anthropic does: for the tokens
// it was given, and with a 401 for anything else.
func fakeAnthropic(t *testing.T, accepts ...string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for _, good := range accepts {
			if r.Header.Get("Authorization") == "Bearer "+good {
				w.Write([]byte(`{"account":{"email_address":"someone@example.com"}}`))
				return
			}
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"OAuth access token is invalid."}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
}

// A token that has died is worth saying so here: an agent given it fails with
// a 401 that explains nothing, hours later.
func TestAuthStatusChecksTheStoredTokens(t *testing.T) {
	isolate(t)
	fakeAnthropic(t, "sk-ant-oat01-good")
	if _, err := run(t, "sk-ant-oat01-good\n", "auth", "claude", "--token-stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "sk-ant-oat01-revoked\n", "auth", "claude", "--token-stdin", "--account", "work"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out,
		"Claude Code   2 account(s): default (default), work",
		"valid, saved "+time.Now().Format(time.DateOnly),
		"rejected, run agentbox auth claude --account work",
	)
}

// A token stored before AgentBox recorded the date keeps working, and says
// outright that it doesn't know how old it is.
func TestAuthStatusOnATokenStoredBeforeTheDateWasKept(t *testing.T) {
	home := isolate(t)
	fakeAnthropic(t, "sk-ant-oat01-old")
	dir := filepath.Join(home, ".config", "agentbox", "credentials", "claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.token"), []byte("sk-ant-oat01-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "valid, saved on an unknown date")
	if out, _ := run(t, "", "auth", "claude", "list"); !strings.Contains(out, "unknown") {
		t.Errorf("auth claude list shows no date for it:\n%s", out)
	}
}

// Not being able to ask is not an answer: a machine that can't reach Anthropic
// must not send anyone off to log in again.
func TestAuthStatusSaysWhenItCouldNotCheck(t *testing.T) {
	isolate(t) // points the check at a port nothing listens on
	if _, err := run(t, "sk-ant-oat01-fine\n", "auth", "claude", "--token-stdin"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "couldn't be checked, saved "+time.Now().Format(time.DateOnly))
	if strings.Contains(out, "rejected") {
		t.Errorf("a token nobody could check was called rejected:\n%s", out)
	}
}
