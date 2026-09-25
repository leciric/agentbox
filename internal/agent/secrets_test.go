package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// recordingIncus is a stand-in incus that keeps every file AgentBox writes into
// an agent, so what really arrives inside a machine can be read back. Its base
// image is ready and one agent is running.
//
// incus.WriteFile runs `incus exec <instance> -T -- sh -c <script> sh <path>
// <uid:gid> <mode>` with the content on stdin; that shape is what the exec case
// below recognises.
func recordingIncus(t *testing.T, instances string) (incus.Client, string) {
	t.Helper()
	files := t.TempDir()
	t.Setenv("INCUS_FILES", files)
	t.Setenv("INCUS_CONFIG_LOG", filepath.Join(files, "_config.log"))
	return fakeIncus(t, `case "$1" in
  list) echo '`+instances+`' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  config) echo "$*" >> "$INCUS_CONFIG_LOG" ;;
  exec)
    if [ "$5" = "sh" ] && [ "$6" = "-c" ] && [ $# -eq 11 ]; then
      dest="$INCUS_FILES/$2$9"
      mkdir -p "$(dirname "$dest")"
      cat > "$dest"
      chmod "${11}" "$dest"
    fi ;;
esac
exit 0`), files
}

// configLog returns the "config ..." commands issued to a recordingIncus.
func configLog(t *testing.T, files string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(files, "_config.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

const oneRunningAgent = `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]`

// inAgent reads a file AgentBox wrote inside an agent.
func inAgent(t *testing.T, files, instance, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(files, instance, path))
	if err != nil {
		t.Fatalf("%s was not written into %s: %v", path, instance, err)
	}
	return string(content)
}

// TestCreateDeliversSecrets checks what an agent has the moment it exists: the
// secrets file, sourced by the env file, and the names in its brief.
func TestCreateDeliversSecrets(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()
	if _, err := f.m.Secrets.Set(ctx, "hello-stack", "", "OPENAI_API_KEY", "sk-project"); err != nil {
		t.Fatal(err)
	}

	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err != nil {
		t.Fatal(err)
	}

	secretsFile := inAgent(t, files, a.Instance, "/home/dev/.config/agentbox/secrets.env")
	if !strings.Contains(secretsFile, "export OPENAI_API_KEY='sk-project'") {
		t.Errorf("the secrets file doesn't carry the secret:\n%s", secretsFile)
	}
	if mode := fileMode(t, files, a.Instance, "/home/dev/.config/agentbox/secrets.env"); mode != 0o600 {
		t.Errorf("the secrets file is mode %o, want 600", mode)
	}

	// The env file every shell, tmux window and ACP adapter sources has to
	// pull the secrets in; otherwise the file above is just a file.
	env := inAgent(t, files, a.Instance, "/home/dev/.config/agentbox/env")
	if !strings.Contains(env, ". '/home/dev/.config/agentbox/secrets.env'") {
		t.Errorf("the env file doesn't source the secrets file:\n%s", env)
	}
	if strings.Contains(env, "sk-project") {
		t.Error("the env file holds the secret's value; it belongs in the secrets file alone")
	}

	// The brief names it, so the AI tool knows the variable exists — and is
	// told the rule about it. No value anywhere near it.
	brief := inAgent(t, files, a.Instance, "/home/dev/.claude/CLAUDE.md")
	if !strings.Contains(brief, "$OPENAI_API_KEY") {
		t.Errorf("the brief doesn't name the secret:\n%s", brief)
	}
	if !strings.Contains(brief, "Never print, commit or echo") {
		t.Errorf("the brief doesn't carry the rule about secrets:\n%s", brief)
	}
	if strings.Contains(brief, "sk-project") {
		t.Error("the brief carries the value")
	}
}

// TestRewriteSecretsReachesRunningAgents is the path a change takes: the whole
// file is written again, so a new secret arrives and a removed one leaves.
func TestRewriteSecretsReachesRunningAgents(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()

	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/home/dev/.config/agentbox/secrets.env"
	if got := inAgent(t, files, a.Instance, path); strings.Contains(got, "export ") {
		t.Errorf("an agent with no secrets got some:\n%s", got)
	}

	// Added after the agent existed: the project's, and one of its own.
	if _, err := f.m.Secrets.Set(ctx, "hello-stack", "", "SHARED_KEY", "shared"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Secrets.Set(ctx, "hello-stack", a.Name, "OWN_KEY", "own"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.RewriteSecrets(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	got := inAgent(t, files, a.Instance, path)
	if !strings.Contains(got, "export SHARED_KEY='shared'") || !strings.Contains(got, "export OWN_KEY='own'") {
		t.Errorf("a secret added after the agent existed didn't reach it:\n%s", got)
	}

	// Removed: the file is written again without it. This is the part a
	// file-per-secret design would get wrong.
	if err := f.m.Secrets.Remove(ctx, "hello-stack", "", "SHARED_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.RewriteSecrets(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	got = inAgent(t, files, a.Instance, path)
	if strings.Contains(got, "SHARED_KEY") {
		t.Errorf("a removed secret is still in the agent:\n%s", got)
	}
	if !strings.Contains(got, "export OWN_KEY='own'") {
		t.Errorf("removing the project's secret took the agent's own:\n%s", got)
	}
}

// TestStartWritesSecrets checks that an agent which was stopped while secrets
// changed catches up when it starts, before its tmux session does.
func TestStartWritesSecrets(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()

	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(files, a.Instance, "home/dev/.config/agentbox/secrets.env")
	if err := os.Remove(path); err != nil { // as if the machine had been stopped since
		t.Fatal(err)
	}
	if _, err := f.m.Secrets.Set(ctx, "hello-stack", "", "LATE_KEY", "late"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := inAgent(t, files, a.Instance, "/home/dev/.config/agentbox/secrets.env"); !strings.Contains(got, "export LATE_KEY='late'") {
		t.Errorf("starting the agent didn't write the secrets it now has:\n%s", got)
	}
}

// TestRewriteSecretsSkipsWhatIsntRunning checks that a stopped agent is not an
// error: it gets the file when it starts.
func TestRewriteSecretsSkipsWhatIsntRunning(t *testing.T) {
	inc, files := recordingIncus(t, `[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]`)
	f := setup(t, inc)
	ctx := context.Background()
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "none",
		Branch: "agentbox/agent-01", Worktree: filepath.Join(t.TempDir(), "agent-01"), Status: state.AgentReady}
	if err := f.st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Secrets.Set(ctx, "hello-stack", "", "KEY", "value"); err != nil {
		t.Fatal(err)
	}

	if err := f.m.RewriteSecrets(ctx, "hello-stack"); err != nil {
		t.Errorf("RewriteSecrets() with a stopped agent = %v, want no error", err)
	}
	if _, err := os.Stat(filepath.Join(files, a.Instance)); err == nil {
		t.Error("something was written into a stopped agent")
	}
}

func fileMode(t *testing.T, files, instance, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(filepath.Join(files, instance, path))
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
