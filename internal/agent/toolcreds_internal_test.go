package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

func claudeAgent(account string) state.Agent {
	return state.Agent{Project: "sai", Name: "agent-01", Instance: "ab-sai-agent-01", AI: "claude", ClaudeAccount: account,
		Worktree: "/w", Interface: state.InterfaceCLI}
}

// A Claude Code on an AgentBox account starts without the variables that
// would replace that account, in the chat and in the terminal alike; one with
// no account keeps them, since then they are how it signs in.
func TestAToolOnAnAccountStartsWithoutItsCredentialVariables(t *testing.T) {
	t.Parallel()
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}}
	m.User.Name = "dev"
	if err := m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}

	window, err := m.toolWindowScript(context.Background(), claudeAgent("work"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-u ANTHROPIC_API_KEY ", "-u ANTHROPIC_AUTH_TOKEN ", "-u ANTHROPIC_BASE_URL ", "-u CLAUDE_CODE_USE_BEDROCK ", "BASH_ENV='\\''/home/dev/.config/agentbox/bash_env'\\'' claude"} {
		if !strings.Contains(window, want) {
			t.Errorf("the terminal's command doesn't have %q:\n%s", want, window)
		}
	}

	// No account stored for it: an API key secret is how it signs in.
	window, err = m.toolWindowScript(context.Background(), claudeAgent("personal"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(window, "env -u") {
		t.Errorf("a Claude Code with no account lost its credential variables:\n%s", window)
	}

	// Codex and OpenCode: only once AgentBox has signed them in.
	codex := state.Agent{AI: "codex"}
	if hide, _ := m.toolHides(codex); hide != nil {
		t.Errorf("Codex with no login hides %v", hide)
	}
	if err := m.Creds.SaveToolAccount("codex", "", "work", []byte(`{"OPENAI_API_KEY":"sk"}`)); err != nil {
		t.Fatal(err)
	}
	if hide, _ := m.toolHides(codex); !strings.Contains(strings.Join(hide, " "), "OPENAI_API_KEY") {
		t.Errorf("Codex on its login hides %v", hide)
	}
	// On an account that was removed since, the variables are how it signs in.
	if hide, _ := m.toolHides(state.Agent{AI: "codex", CodexAccount: "gone"}); hide != nil {
		t.Errorf("Codex on an account AgentBox doesn't have hides %v", hide)
	}
}

// The chat's command removes them in the env that execs mise, with no shell
// after it that could source them back.
func TestTheChatStartsWithoutTheCredentialVariables(t *testing.T) {
	t.Parallel()
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}, Incus: fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-sai-agent-01","status":"Running"}]' ;;
esac`), Store: openInternalStore(t)}
	m.User.Name = "dev"
	if err := m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	a := claudeAgent("work")
	a.Interface = state.InterfaceChat
	cmd, err := m.ChatCommand(context.Background(), a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	script := cmd.Args[len(cmd.Args)-1]
	if !strings.Contains(script, "exec env -u ANTHROPIC_API_KEY -u ") || !strings.Contains(script, " BASH_ENV='/home/dev/.config/agentbox/bash_env' mise exec 'npm:@agentclientprotocol/claude-agent-acp@") {
		t.Errorf("the chat's command doesn't drop the credential variables just before mise:\n%s", script)
	}
}

// The whole path in a real bash: the agent's login shell sources its secrets,
// the AI tool is started the way AgentBox starts it, and then the tool has no
// ANTHROPIC_API_KEY while the shells it starts for commands (bash -c, as the
// Bash tool does) have the secret. A launcher written in bash, which bash runs
// by its shebang and not as a command, doesn't get it back through BASH_ENV.
func TestTheSecretIsInTheToolsShellsButNotInTheTool(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}}
	m.User.Name = "dev"
	if err := m.Creds.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	local := func(s string) string {
		s = strings.ReplaceAll(s, m.SecretsPath(), filepath.Join(dir, "secrets.env"))
		s = strings.ReplaceAll(s, m.BashEnvPath(), filepath.Join(dir, "bash_env"))
		return strings.ReplaceAll(s, "/home/dev/", dir+"/")
	}
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("secrets.env", "export ANTHROPIC_API_KEY='sk-ant-api03-the-secret'\n", 0o600)
	write("bash_env", local(m.bashEnv()), 0o644)
	// The tool: what it has itself, and what its Bash tool's shell has.
	write("tool", "#!/bin/sh\necho \"tool=${ANTHROPIC_API_KEY-none} token=${CLAUDE_CODE_OAUTH_TOKEN-none}\"\nbash -c 'echo \"bash=${ANTHROPIC_API_KEY-none}\"'\n", 0o755)
	write("launcher", "#!/usr/bin/env bash\nexec \"$(dirname \"$0\")/tool\"\n", 0o755)

	hide, err := m.toolHides(claudeAgent("work"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"tool", "launcher"} {
		script := ". ./secrets.env; export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-work; exec " + local(m.withoutCredentials(hide)) + "./" + tool
		cmd := exec.Command("bash", "-c", script)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", tool, err, out)
		}
		want := "tool=none token=sk-ant-oat01-work\nbash=sk-ant-api03-the-secret\n"
		if string(out) != want {
			t.Errorf("through %s:\n%s\nwant:\n%s", tool, out, want)
		}
	}
}

// The script reads a real process's environment, hashing the values: a
// Claude Code with an ANTHROPIC_API_KEY runs on it, whatever account's token
// it also has.
func TestCredentialScriptReadsARealProcess(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	// A process named claude, as the native binary is.
	claude := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(sleep, claude); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(claude, "30")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-work", "ANTHROPIC_API_KEY=sk-ant-api03-key", "ANTHROPIC_BASE_URL=https://proxy"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	// A login file is reported by its hash alone.
	authFile := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"OPENAI_API_KEY":"sk-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("bash", "-c", credentialScript(os.Getuid(), []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"}, authFile)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "sk-ant") || strings.Contains(string(out), "sk-secret") {
		t.Fatal("a value left the machine")
	}
	if got := authFileSum(string(out)); got != sha256Hex(`{"OPENAI_API_KEY":"sk-secret"}`) {
		t.Errorf("the login file's hash = %q", got)
	}
	for _, p := range parseToolProcesses(string(out)) {
		if p.pid != cmd.Process.Pid {
			continue
		}
		if !isTool("claude", p.argv) || p.ppid != os.Getpid() {
			t.Errorf("our claude: %+v", p)
		}
		variable, others := credentialOf("claude", p.env)
		if variable != "ANTHROPIC_API_KEY" || strings.Join(others, " ") != "ANTHROPIC_BASE_URL" || p.env["CLAUDE_CODE_OAUTH_TOKEN"] != sha256Hex("sk-ant-oat01-work") {
			t.Errorf("our claude runs on %q, with %v and %v", variable, others, p.env)
		}
		return
	}
	t.Errorf("our claude isn't in:\n%s", out)
}

// ToolCredential takes the topmost Claude Code (a claude the agent ran in its
// Bash tool is its code), and names the account whose token it holds.
func TestToolCredentialNamesTheAccount(t *testing.T) {
	t.Parallel()
	sum := sha256Hex("sk-ant-oat01-work")
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}, Incus: fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-sai-agent-01","status":"Running"}]' ;;
  exec) printf 'P 10 1 node\001/x/.bin/claude-agent-acp\nP 11 10 /x/claude\001--verbose\nE CLAUDE_CODE_OAUTH_TOKEN `+sum+`\nP 12 11 bash\001-c\001claude -p hi\nP 13 12 /x/claude\001-p\nE ANTHROPIC_API_KEY abc\n' ;;
esac`)}
	for _, account := range []string{"personal", "work"} {
		if err := m.Creds.SaveClaudeToken(account, "sk-ant-oat01-"+account); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.ToolCredential(context.Background(), claudeAgent("work"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Running || got.Variable != "CLAUDE_CODE_OAUTH_TOKEN" || got.Account != "work" || len(got.Others) != 0 {
		t.Errorf("ToolCredential = %+v", got)
	}
}

// A Codex agent's account is the one whose login file it has; OpenCode's is
// the account each provider's entry came from.
func TestToolCredentialNamesCodexAndOpenCodeAccounts(t *testing.T) {
	t.Parallel()
	creds := credentials.Store{Dir: t.TempDir()}
	for _, name := range []string{"home", "work"} {
		if err := creds.SaveToolAccount("codex", "", name, []byte(`{"OPENAI_API_KEY":"sk-`+name+`"}`)); err != nil {
			t.Fatal(err)
		}
		if err := creds.SaveToolAccount("opencode", "anthropic", name, []byte(`{"type":"api","key":"`+name+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	opencode, _, err := creds.OpenCodeAuth(map[string]string{"anthropic": "work"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		agent state.Agent
		file  string
		want  string
	}{
		{state.Agent{AI: "codex", Instance: "i", CodexAccount: "home"}, `{"OPENAI_API_KEY":"sk-work"}`, "work"},
		{state.Agent{AI: "codex", Instance: "i"}, `{"OPENAI_API_KEY":"refreshed"}`, ""},
		{state.Agent{AI: "opencode", Instance: "i", OpenCodeAccounts: state.AccountMap{"anthropic": "work"}}, string(opencode), "anthropic: work"},
	} {
		argv := `/x/codex-acp`
		if tc.agent.AI == "opencode" {
			argv = `/x/opencode\001acp`
		}
		m := &Manager{Creds: creds, Incus: fakeIncus(t, `case "$1" in
  list) echo '[{"name":"i","status":"Running"}]' ;;
  exec) printf 'P 10 1 `+argv+`\nF `+sha256Hex(tc.file)+`\n' ;;
esac`)}
		got, err := m.ToolCredential(context.Background(), tc.agent)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Running || got.Variable != "" || got.Account != tc.want {
			t.Errorf("%s with %s: ToolCredential = %+v, want the account %q", tc.agent.AI, tc.file, got, tc.want)
		}
	}
}

func TestIsTool(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tool string
		argv []string
		want bool
	}{
		{"claude", []string{"/x/claude-agent-sdk-linux-x64/claude", "--output-format", "stream-json"}, true},
		{"claude", []string{"node", "/x/claude-agent-acp/dist/index.js"}, false},
		{"claude", []string{"node", "/x/.bin/claude-agent-acp"}, false},
		{"claude", []string{"node", "/x/@anthropic-ai/claude-code/cli.js"}, true},
		{"claude", []string{"bash", "-c", "claude -p hi"}, false},
		{"codex", []string{"/x/codex-acp"}, true},
		{"opencode", []string{"node", "/x/bin/opencode", "acp"}, true},
		{"opencode", []string{"/x/opencode", "acp"}, true},
	} {
		if got := isTool(tc.tool, tc.argv); got != tc.want {
			t.Errorf("isTool(%s, %q) = %v", tc.tool, tc.argv, got)
		}
	}
}

// The chat says its own name, which bills the 5-hour window like the VS Code
// extension and well under the Agent SDK's default; the terminal interface and
// other tools say nothing.
func TestClaudeChatsReportTheAgentboxEntrypoint(t *testing.T) {
	t.Parallel()
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}, Incus: fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-sai-agent-01","status":"Running"}]' ;;
esac`), Store: openInternalStore(t)}
	m.User.Name = "dev"
	script := func(ai string) string {
		a := claudeAgent("")
		a.AI, a.Interface = ai, state.InterfaceChat
		cmd, err := m.ChatCommand(context.Background(), a, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		return cmd.Args[len(cmd.Args)-1]
	}
	if s := script("claude"); !strings.Contains(s, "&& CLAUDE_CODE_ENTRYPOINT=agentbox exec ") {
		t.Errorf("the Claude chat doesn't set the entrypoint:\n%s", s)
	}
	for _, ai := range []string{"codex", "opencode"} {
		if s := script(ai); strings.Contains(s, "CLAUDE_CODE_ENTRYPOINT") {
			t.Errorf("the %s chat sets the entrypoint:\n%s", ai, s)
		}
	}
	window, err := m.toolWindowScript(context.Background(), claudeAgent(""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(window, "CLAUDE_CODE_ENTRYPOINT") {
		t.Errorf("the terminal interface sets the entrypoint:\n%s", window)
	}
}

// OpenCode 2 keeps every credential it is given, and `auth import` skips one
// it already has: a sync gives it exactly the accounts asked for, logging out
// the ones it replaced so a removed or renamed account can't keep answering.
// An export that didn't change touches nothing.
func TestSyncOpenCodeImportLogsOutWhatItReplaced(t *testing.T) {
	t.Parallel()
	var marker []byte
	var ran []string
	run := func(_ context.Context, args []string, stdin io.Reader) error {
		ran = append(ran, strings.Join(args, " "))
		return nil
	}
	read := func() ([]byte, error) { return marker, nil }
	write := func(b []byte) error { marker = b; return nil }
	ctx := context.Background()
	export := []byte(`[{"id":"a"}]`)

	if err := syncOpenCodeImport(ctx, read, write, run, export, map[string]string{"openai": "work"}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || !strings.HasPrefix(ran[0], "auth import") {
		t.Fatalf("the first sync ran %q, want only an import", ran)
	}
	ran = nil
	if err := syncOpenCodeImport(ctx, read, write, run, export, map[string]string{"openai": "work"}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Fatalf("an unchanged export ran %q", ran)
	}

	// The same provider under another label: the new one is imported, then
	// the old label taken out.
	ran = nil
	if err := syncOpenCodeImport(ctx, read, write, run, []byte(`[{"id":"b"}]`), map[string]string{"openai": "personal"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"auth import --standalone", "auth logout --standalone openai work"}
	if strings.Join(ran, "|") != strings.Join(want, "|") {
		t.Fatalf("a renamed account ran %q, want %q", ran, want)
	}

	// Every account removed: an empty export imports nothing and out goes the
	// label that was there.
	ran = nil
	if err := syncOpenCodeImport(ctx, read, write, run, []byte("[]"), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	want = []string{"auth import --standalone", "auth logout --standalone openai personal"}
	if strings.Join(ran, "|") != strings.Join(want, "|") {
		t.Fatalf("removing every account ran %q, want %q", ran, want)
	}
}

// TestSyncOpenCodeImportReplacesARotatedKey: a new key under the same label has
// the same credential id, which `auth import` skips, so the old one is logged
// out first. A provider whose credential didn't change is left alone, since
// the machine may hold a token OpenCode refreshed since.
func TestSyncOpenCodeImportReplacesARotatedKey(t *testing.T) {
	t.Parallel()
	var marker []byte
	var ran []string
	run := func(_ context.Context, args []string, stdin io.Reader) error {
		ran = append(ran, strings.Join(args, " "))
		return nil
	}
	read := func() ([]byte, error) { return marker, nil }
	write := func(b []byte) error { marker = b; return nil }
	ctx := context.Background()
	used := map[string]string{"anthropic": "work", "openai": "work"}
	export := func(key string) []byte {
		return []byte(`[{"id":"cred_agentbox_anthropic_work","integrationID":"anthropic","label":"work","active":true,"value":{"type":"key","key":"` + key + `"}},` +
			`{"id":"cred_agentbox_openai_work","integrationID":"openai","label":"work","active":true,"value":{"type":"oauth","refresh":"r"}}]`)
	}

	if err := syncOpenCodeImport(ctx, read, write, run, export("one"), used); err != nil {
		t.Fatal(err)
	}
	ran = nil
	if err := syncOpenCodeImport(ctx, read, write, run, export("two"), used); err != nil {
		t.Fatal(err)
	}
	want := []string{"auth logout --standalone anthropic work", "auth import --standalone"}
	if strings.Join(ran, "|") != strings.Join(want, "|") {
		t.Fatalf("a rotated key ran %q, want %q", ran, want)
	}
}

// TestOpenCode2InstallSaysWhenThereIsNoNetwork: OpenCode 2 isn't in the image,
// so an agent's first start under it installs it, and a machine that can't
// reach npm's registry is told that, not handed npm's log.
func TestOpenCode2InstallSaysWhenThereIsNoNetwork(t *testing.T) {
	t.Parallel()
	m := &Manager{Incus: fakeIncus(t, `
case "$*" in
  *"command -v"*) exit 1 ;;
  *"npm install"*) printf 'npm error code ENOTFOUND\nnpm error network request failed\n'; exit 1 ;;
esac
exit 0
`), User: image.User{Name: "dev", UID: 1000, GID: 1000}}
	a := state.Agent{Project: "hello-stack", Name: "agent-01", AI: "opencode", Instance: "ab-hello-stack-agent-01"}
	err := m.ensureOpenCode2InAgent(t.Context(), a)
	if err == nil {
		t.Fatal("an install with no network succeeded")
	}
	if !strings.Contains(err.Error(), "needs the network") || !strings.Contains(err.Error(), "ENOTFOUND") || !strings.Contains(err.Error(), "switch back to OpenCode 1") {
		t.Errorf("err = %v", err)
	}
}

func TestNpmNetworkError(t *testing.T) {
	t.Parallel()
	for out, want := range map[string]string{
		"npm error code ECONNREFUSED\nnpm error errno ECONNREFUSED": "ECONNREFUSED",
		"npm error code EAI_AGAIN":                                  "EAI_AGAIN",
		"npm error code E404\nnpm error 404 Not Found":              "",
		"": "",
	} {
		if got := npmNetworkError(out); got != want {
			t.Errorf("npmNetworkError(%q) = %q, want %q", out, got, want)
		}
	}
}

// TestOpenCode2InstallChecksThePinAndTakesALock: an agent with an older
// OpenCode 2 than image.OpenCode2Version isn't "installed", and the install
// runs under a lock so two starts don't write the same directory at once.
func TestOpenCode2InstallChecksThePinAndTakesALock(t *testing.T) {
	t.Parallel()
	log := filepath.Join(t.TempDir(), "calls")
	m := &Manager{Incus: fakeIncus(t, `printf '%s\n' "$*" >> `+shellQuote(log)+`
case "$*" in *"command -v"*) exit 1 ;; esac
exit 0
`), User: image.User{Name: "dev", UID: 1000, GID: 1000}}
	a := state.Agent{Project: "hello-stack", Name: "agent-01", AI: "opencode", Instance: "ab-hello-stack-agent-01"}
	if err := m.ensureOpenCode2InAgent(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(raw)
	if !strings.Contains(calls, "package.json") || !strings.Contains(calls, image.OpenCode2Version) {
		t.Errorf("the installed version isn't compared with the pin: %s", calls)
	}
	if !strings.Contains(calls, "flock") {
		t.Errorf("the install takes no lock: %s", calls)
	}
}
