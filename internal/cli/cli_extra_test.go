package cli_test

// More commands exercised through the same daemon harness as cli_test.go:
// argument validation, the exact error a bad argument gets, and the exact
// text a command prints when it succeeds. This focuses on the commands
// cli_test.go doesn't already cover.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/testutil"
)

// fakeHub answers the hub routes login, env list/add/rm use: enough to drive
// them end to end without a real hub.
func fakeHub(t *testing.T) string {
	t.Helper()
	envs := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "hub_tok_1",
				"user":  map[string]any{"id": "u1", "email": "dev@example.com", "name": "Dev"},
			})
		case r.URL.Path == "/v1/environments" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(envs)
		case r.URL.Path == "/v1/environments" && r.Method == http.MethodPost:
			var in struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			envs = append(envs, map[string]any{"id": "env_1", "name": in.Name})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"environment": map[string]any{"id": "env_1", "name": in.Name},
				"token":       "env_tok_1",
			})
		case strings.HasPrefix(r.URL.Path, "/v1/environments/") && r.Method == http.MethodDelete:
			envs = nil
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBaseShowWithNoSavedBase(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "base", "show", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack has no saved base: new agents start from")

	if _, err := run(t, "", "base", "show", "no-such-project"); err == nil {
		t.Error("base show of an unknown project succeeded")
	}
	if _, err := run(t, "", "base", "save", "not-a-slash"); err == nil || !strings.Contains(err.Error(), "use <project>/<agent>") {
		t.Errorf("base save without project/agent: got %v, want an invalid-agent error", err)
	}
	if _, err := run(t, "", "base", "revert", "no-such-project"); err == nil {
		t.Error("base revert on an unknown project succeeded")
	}
}

func TestSnapshotFamilyOnAnUnknownAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	for _, args := range [][]string{
		{"snapshot", "pawly/agent-01"},
		{"snapshots", "pawly/agent-01"},
		{"restore", "pawly/agent-01", "before"},
		{"fork", "pawly/agent-01"},
	} {
		if _, err := run(t, "", args...); err == nil {
			t.Errorf("agentbox %s on an unknown agent succeeded", strings.Join(args, " "))
		}
	}
}

func TestJobsShowAndCancelUnknown(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "jobs", "no-such-job"); err == nil {
		t.Error("agentbox jobs no-such-job succeeded")
	}
	if _, err := run(t, "", "jobs", "cancel", "no-such-job"); err == nil {
		t.Error("agentbox jobs cancel no-such-job succeeded")
	}
}

func TestDaemonStopWithNoneRunning(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "daemon", "stop"); err == nil || !strings.Contains(err.Error(), "no daemon is answering on") {
		t.Errorf("daemon stop with none running: got %v", err)
	}
}

func TestDaemonInstallPrint(t *testing.T) {
	isolate(t)
	out, err := run(t, "", "daemon", "install", "--print")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Description=AgentBox daemon", "ExecStart=", "WantedBy=default.target")
}

func TestHostSetupPrintsTheScriptWithoutRoot(t *testing.T) {
	isolate(t)
	out, err := run(t, "", "host", "setup", "--print")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Error("host setup --print produced nothing")
	}
}

func TestHostSetupRejectsABadBridgeSubnet(t *testing.T) {
	isolate(t)
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t, "", "host", "setup", "--user", me.Username, "--bridge-subnet", "not-an-address")
	if err == nil || !strings.Contains(err.Error(), "wants an address and prefix, like 10.87.0.1/24") {
		t.Errorf("host setup --bridge-subnet not-an-address: got %v", err)
	}
}

func TestHostCheck(t *testing.T) {
	isolate(t)
	startDaemon(t)
	// The daemon's fake incus answers nothing is set up, so this is expected
	// to report it isn't ready (exit code 1) rather than fail outright.
	out, err := run(t, "", "host", "check")
	if err == nil {
		t.Log("host check reported ready in this environment")
	}
	if len(out) == 0 {
		t.Error("host check printed nothing")
	}
}

func TestGitHubAccountCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "github-account", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "no GitHub account picked, and none is stored")

	if _, err := run(t, "", "github-account", "hello-stack", "work", "--clear"); err == nil || !strings.Contains(err.Error(), "ask for opposite things") {
		t.Errorf("github-account with both an account and --clear: got %v", err)
	}

	if _, err := run(t, "", "github-account", "no-such-project"); err == nil {
		t.Error("github-account of an unknown project succeeded")
	}

	if _, err := run(t, "", "github-account", "hello-stack/agent-01", "work"); err == nil {
		t.Error("github-account for an unknown agent succeeded")
	}
}

func TestGitHubAccountMoveAgentsFlag(t *testing.T) {
	home := isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "agentbox", "credentials", "github")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"work", "other"} {
		if err := os.WriteFile(filepath.Join(dir, account+".token"), []byte("gho_"+account+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// --move-agents belongs to a project, not to one agent's own account.
	if _, err := run(t, "", "github-account", "hello-stack/agent-01", "work", "--move-agents"); err == nil ||
		!strings.Contains(err.Error(), "moves a project's agents, not one agent's own account") {
		t.Errorf("github-account --move-agents on an agent: got %v", err)
	}

	// With nobody on the old account, --move-agents still answers without a prompt.
	out, err := run(t, "", "github-account", "hello-stack", "work", "--move-agents")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "move to the new one")

	// Without the flag and nobody left on the old account, no prompt blocks this.
	out, err = run(t, "", "github-account", "hello-stack", "other")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "keep the account they were created with")
}

func TestClaudeAccountAllowFlags(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "claude-account", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "no Claude Code account picked", "hello-stack may use every Claude Code account")

	// --allow-all combined with --allow is refused before anything is sent.
	if _, err := run(t, "", "claude-account", "hello-stack", "--allow-all", "--allow", "work"); err == nil ||
		!strings.Contains(err.Error(), "don't combine it with") {
		t.Errorf("claude-account --allow-all --allow work: got %v", err)
	}

	for _, account := range []string{"work", "personal"} {
		if _, err := run(t, "sk-ant-oat01-"+account+"\n", "auth", "claude", "--token-stdin", "--account", account); err != nil {
			t.Fatal(err)
		}
	}
	out, err = run(t, "", "claude-account", "hello-stack", "--allow", "work,personal")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack may use the Claude Code accounts work, personal")

	// The account list can't apply to one agent.
	if _, err := run(t, "", "claude-account", "hello-stack/agent-01", "--allow", "work"); err == nil ||
		!strings.Contains(err.Error(), "belong to a project, not to one agent") {
		t.Errorf("claude-account --allow on an agent: got %v", err)
	}

	// Removing everything the list has, with nothing left, is refused.
	if _, err := run(t, "", "claude-account", "hello-stack", "--allow-remove", "work,personal"); err == nil ||
		!strings.Contains(err.Error(), "no Claude Code account at all") {
		t.Errorf("claude-account --allow-remove emptying the list: got %v", err)
	}
}

func TestClaudeAccountMoveAgentsFlag(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"work", "personal"} {
		if _, err := run(t, "sk-ant-oat01-"+account+"\n", "auth", "claude", "--token-stdin", "--account", account); err != nil {
			t.Fatal(err)
		}
	}

	// --move-agents belongs to a project, not to one agent's own account.
	if _, err := run(t, "", "claude-account", "hello-stack/agent-01", "work", "--move-agents"); err == nil ||
		!strings.Contains(err.Error(), "moves a project's agents, not one agent's own account") {
		t.Errorf("claude-account --move-agents on an agent: got %v", err)
	}

	// With nobody on the old account, --move-agents still answers without a prompt.
	out, err := run(t, "", "claude-account", "hello-stack", "work", "--move-agents")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "move to the new one")

	// Without the flag and nobody left on the old account, no prompt blocks this.
	out, err = run(t, "", "claude-account", "hello-stack", "personal")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "keep the account they were created with")
}

func TestQuestionsAutonomyAndAnswer(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "questions", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No agent is waiting to be told anything.")

	if _, err := run(t, "", "answer", "hello-stack", "q1", "   "); err == nil || !strings.Contains(err.Error(), "say what the agent should do") {
		t.Errorf("answer with a blank answer: got %v", err)
	}
	if _, err := run(t, "", "answer", "hello-stack", "no-such-question", "do it"); err == nil {
		t.Error("answering a question that doesn't exist succeeded")
	}

	out, err = run(t, "", "autonomy", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: ask")

	out, err = run(t, "", "autonomy", "hello-stack", "on")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack: on")

	if _, err := run(t, "", "autonomy", "no-such-project"); err == nil {
		t.Error("autonomy of an unknown project succeeded")
	}
}

func TestTokensCmdBadSince(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "tokens", "--since", "soon"); err == nil || !strings.Contains(err.Error(), `isn't a stretch of time`) {
		t.Errorf("tokens --since soon: got %v", err)
	}
	out, err := run(t, "", "tokens")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Nothing spent")
}

func TestLimitsOnAnUnknownAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "limits", "pawly/agent-01"); err == nil {
		t.Error("limits of an unknown agent succeeded")
	}
	if _, err := run(t, "", "limits", "pawly/agent-01", "--cpu", "4"); err == nil {
		t.Error("limits --cpu of an unknown agent succeeded")
	}
}

func TestBrowserStatusOnAnUnknownAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "browser", "status", "pawly/agent-01"); err == nil {
		t.Error("browser status of an unknown agent succeeded")
	}
	if _, err := run(t, "", "browser", "open", "pawly/agent-01", "http://localhost:3000"); err == nil {
		t.Error("browser open of an unknown agent succeeded")
	}
}

func TestAskRefusesAnEmptyQuestion(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "ask", "   "); err == nil || !strings.Contains(err.Error(), "say what you need decided") {
		t.Errorf("ask with a blank question: got %v", err)
	}
}

func TestWSLBridgeWaitTimesOut(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "wsl-bridge", "--wait", "50ms"); err == nil || !strings.Contains(err.Error(), "isn't answering") {
		t.Errorf("wsl-bridge --wait with no daemon: got %v", err)
	}
}

func TestRemoteConnectNeedsAToken(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "remote", "connect", "https://hub.example"); err == nil || !strings.Contains(err.Error(), "pass the environment's token") {
		t.Errorf("remote connect with no token: got %v", err)
	}
}

func TestEnvAndLogoutWithNoHub(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "env", "list"); err == nil || !strings.Contains(err.Error(), "not signed in to a hub") {
		t.Errorf("env list with no hub: got %v", err)
	}
	if _, err := run(t, "", "logout"); err == nil || !strings.Contains(err.Error(), "not signed in to that hub") {
		t.Errorf("logout with no hub: got %v", err)
	}
	if _, err := run(t, "", "env", "add", "myenv"); err == nil {
		t.Error("env add with no hub succeeded")
	}
}

func TestEnvFlagRefusesLocalOnlyCommands(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{
		{"--env", "prod", "shell", "pawly/agent-01"},
		{"--env", "prod", "auth", "status"},
		{"--env", "prod", "host", "check"},
	} {
		if _, err := run(t, "", args...); err == nil || !strings.Contains(err.Error(), "works on this machine only, not with --env") {
			t.Errorf("agentbox %s: got %v, want a local-only error", strings.Join(args, " "), err)
		}
	}
}

func TestChatAndInterfaceCmds(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "chat", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No messages yet. Send one with: agentbox chat hello-stack")

	if _, err := run(t, "", "interface", "hello-stack/agent-01"); err == nil {
		t.Error("interface of an unknown agent succeeded")
	}
	if _, err := run(t, "", "chat", "hello-stack/agent-01", "--stop"); err == nil {
		t.Error("chat --stop for an unknown agent succeeded")
	}
}

func TestAgentCmdsOnAnEmptyOrUnknownAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No agents. Create one with: agentbox create <project>")

	for _, args := range [][]string{
		{"title", "hello-stack/agent-01", "New title"},
		{"start", "hello-stack/agent-01"},
		{"stop", "hello-stack/agent-01"},
		{"pause", "hello-stack/agent-01"},
		{"resume", "hello-stack/agent-01"},
		{"destroy", "hello-stack/agent-01"},
		{"diff", "hello-stack/agent-01"},
		{"path", "hello-stack/agent-01"},
		{"shell", "hello-stack/agent-01"},
		{"interface", "hello-stack/agent-01", "chat"},
	} {
		if _, err := run(t, "", args...); err == nil {
			t.Errorf("agentbox %s on an unknown agent succeeded", strings.Join(args, " "))
		}
	}

	if _, err := run(t, "", "exec", "hello-stack/agent-01", "--", "true"); err == nil {
		t.Error("exec on an unknown agent succeeded")
	}

	imgOut, err := run(t, "", "image", "version")
	if err != nil {
		t.Fatal(err)
	}
	if len(imgOut) == 0 {
		t.Error("agentbox image version printed nothing")
	}
}

func TestMediaListAndOtherSubcommands(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "media", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No media yet")

	if _, err := run(t, "", "media", "list", "Not A Project"); err == nil || !strings.Contains(err.Error(), "neither a project nor an agent") {
		t.Errorf("media list on a bad ref: got %v", err)
	}

	if _, err := run(t, "", "media", "add", "hello-stack/agent-01", "somefile.txt"); err == nil {
		t.Error("media add for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "note", "hello-stack/agent-01", "a note"); err == nil {
		t.Error("media note for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "open", "hello-stack/agent-01", "some-id"); err == nil {
		t.Error("media open for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "rm", "hello-stack/agent-01", "some-id"); err == nil {
		t.Error("media rm for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "logs", "hello-stack/agent-01", "--service", "web"); err == nil {
		t.Error("media logs for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "screenshot", "hello-stack/agent-01"); err == nil {
		t.Error("media screenshot for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "record", "start", "hello-stack/agent-01"); err == nil {
		t.Error("media record start for an unknown agent succeeded")
	}
	if _, err := run(t, "", "media", "record", "status", "hello-stack/agent-01"); err == nil {
		t.Error("media record status for an unknown agent succeeded")
	}
}

func TestLoginEnvAndLogoutAgainstAFakeHub(t *testing.T) {
	isolate(t)
	hubURL := fakeHub(t)

	out, err := run(t, "secret123\n", "login", hubURL, "--email", "dev@example.com", "--password-stdin")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Signed in to "+hubURL+" as dev@example.com")

	out, err = run(t, "", "env", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "ENVIRONMENT")

	out, err = run(t, "", "env", "add", "prod")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Added the environment prod", "agentbox remote connect "+hubURL+" --token env_tok_1")

	out, err = run(t, "", "env", "rm", "prod")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Deleted the environment prod")

	if _, err := run(t, "", "env", "rm", "no-such-env"); err == nil || !strings.Contains(err.Error(), "no environment named no-such-env") {
		t.Errorf("env rm of an unknown environment: got %v", err)
	}

	out, err = run(t, "", "logout")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Signed out of "+hubURL)

	if _, err := run(t, "", "env", "list"); err == nil {
		t.Error("env list after logging out succeeded")
	}
}

func TestAndroidCmdsOnAnUnknownAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"android", "status", "hello-stack/agent-01"},
		{"android", "start", "hello-stack/agent-01"},
		{"android", "stop", "hello-stack/agent-01"},
		{"android", "install", "hello-stack/agent-01", "app.apk"},
		{"android", "screenshot", "hello-stack/agent-01"},
		{"android", "record", "start", "hello-stack/agent-01"},
		{"android", "logs", "hello-stack/agent-01"},
	} {
		if _, err := run(t, "", args...); err == nil {
			t.Errorf("agentbox %s on an unknown agent succeeded", strings.Join(args, " "))
		}
	}
}

func TestDesktopMCPNeedsAnAgent(t *testing.T) {
	isolate(t)
	if _, err := os.Stat(api.InAgentSocket); err == nil {
		t.Skip("running inside an agent")
	}
	if _, err := run(t, "", "desktop", "mcp"); err == nil || !strings.Contains(err.Error(), "runs inside an agent") {
		t.Errorf("desktop mcp outside an agent: got %v", err)
	}
}

func TestDesktopOverlayNeedsItsEventsFile(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "desktop", "overlay", "--events", "/no/such/file", "--start", "0"); err == nil {
		t.Error("desktop overlay with a missing events file succeeded")
	}
}

func TestMCPCmdNeedsASocket(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTBOX_SOCKET", "")
	if _, err := run(t, "", "mcp"); err == nil || !strings.Contains(err.Error(), "AGENTBOX_SOCKET isn't set") {
		t.Errorf("mcp with no AGENTBOX_SOCKET: got %v", err)
	}
}

func TestDaemonUninstallWhenNeverInstalled(t *testing.T) {
	isolate(t)
	out, err := run(t, "", "daemon", "uninstall")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Removed agentbox.service")
}

func TestRemoteStatusNotConfigured(t *testing.T) {
	isolate(t)
	startDaemon(t)
	out, err := run(t, "", "remote", "status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Not connected to a hub. Connect with: agentbox remote connect")

	if _, err := run(t, "", "remote", "disconnect"); err == nil {
		t.Log("remote disconnect with nothing connected did not error (accepted as a no-op)")
	}
}

func TestListWithAProjectArgument(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No agents. Create one with: agentbox create <project>")
}

func TestProjectSettingsShowAndValidate(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "project", "branch-prefix", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "agentbox/")

	out, err = run(t, "", "project", "branch-prefix", "hello-stack", "thiago/agentbox/")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "thiago/agentbox/")

	if _, err := run(t, "", "project", "branch-prefix", "no-such-project"); err == nil {
		t.Error("project branch-prefix of an unknown project succeeded")
	}

	out, err = run(t, "", "rollover", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "hello-stack:")

	if _, err := run(t, "", "rollover", "hello-stack", "not-a-number"); err == nil || !strings.Contains(err.Error(), "invalid threshold") {
		t.Errorf("rollover with a bad value: got %v", err)
	}

	out, err = run(t, "", "rollover", "hello-stack", "off")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "off — the conversation is never compacted")

	if _, err := run(t, "", "context-budget", "hello-stack", "not-a-number"); err == nil || !strings.Contains(err.Error(), "invalid budget") {
		t.Errorf("context-budget with a bad value: got %v", err)
	}
	out, err = run(t, "", "context-budget", "hello-stack", "8000")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "8000 tokens a context, 2000 for one agent")

	if _, err := run(t, "", "consolidation", "hello-stack", "not-a-number"); err == nil || !strings.Contains(err.Error(), "invalid consolidation") {
		t.Errorf("consolidation with a bad value: got %v", err)
	}
	out, err = run(t, "", "consolidation", "hello-stack", "off")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "off — its memories are only ever added to")

	out, err = run(t, "", "consolidation-model", "hello-stack", "chat")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "the chat's own model, in the chat's own session")

	if _, err := run(t, "", "project", "model", "no-such-project"); err == nil {
		t.Error("project model of an unknown project succeeded")
	}
}
