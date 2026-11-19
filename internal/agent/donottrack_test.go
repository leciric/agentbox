package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
)

// doNotTrackIncus is recordingIncus that also logs every incus call, in full,
// to the file the returned function reads.
func doNotTrackIncus(t *testing.T) (inc incus.Client, files string, log func() string) {
	t.Helper()
	files = t.TempDir()
	logFile := filepath.Join(t.TempDir(), "calls")
	t.Setenv("INCUS_FILES", files)
	t.Setenv("INCUS_LOG", logFile)
	inc = fakeIncus(t, `printf '%s\n' "$*" >> "$INCUS_LOG"
case "$1" in
  list) echo '`+oneRunningAgent+`' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  exec)
    if [ "$5" = "sh" ] && [ "$6" = "-c" ] && [ $# -eq 11 ]; then
      dest="$INCUS_FILES/$2$9"
      mkdir -p "$(dirname "$dest")"
      cat > "$dest"
      chmod "${11}" "$dest"
    fi ;;
esac
exit 0`)
	return inc, files, func() string {
		t.Helper()
		b, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// TestEveryAgentMachineIsToldNotToTrack: AgentBox copies its released binary
// into each agent as /usr/local/bin/agentbox, so a daemon started there (a
// nested `agentbox host setup`, a test's) would count as an install in the
// usage stats and the update check, unless DO_NOT_TRACK=1 is in everything
// that can start one: the machine's own environment, for `incus exec`; the
// login shells' env file; the Bash tool's bash_env; tmux, which every terminal
// shell comes from; and Claude Code's, which its tools do.
func TestEveryAgentMachineIsToldNotToTrack(t *testing.T) {
	inc, files, log := doNotTrackIncus(t)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("personal", "sk-ant-oat01-personal"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ClaudeAccount: "personal"})
	if err != nil {
		t.Fatal(err)
	}

	calls := log()
	if !strings.Contains(calls, "config set "+a.Instance+" environment.DO_NOT_TRACK=1") {
		t.Errorf("the machine's environment key was never set (what `incus exec` runs with):\n%s", calls)
	}
	if !strings.Contains(calls, "new-session -d -e DO_NOT_TRACK=1 ") {
		t.Errorf("the tmux session wasn't started with DO_NOT_TRACK=1:\n%s", calls)
	}
	for _, c := range []struct{ what, path, want string }{
		{"the env file login shells read", "/home/dev/.config/agentbox/env", "export DO_NOT_TRACK=1\n"},
		{"bash_env, the Bash tool's shells'", "/home/dev/.config/agentbox/bash_env", "export DO_NOT_TRACK=1\n"},
		{"tmux's configuration", "/home/dev/.tmux.conf", "set-environment -g DO_NOT_TRACK 1\n"},
		{"Claude Code's settings.json env", "/home/dev/.claude/settings.json", `"DO_NOT_TRACK": "1"`},
	} {
		if got := inAgent(t, files, a.Instance, c.path); !strings.Contains(got, c.want) {
			t.Errorf("%s doesn't carry %q:\n%s", c.what, strings.TrimSpace(c.want), got)
		}
	}
}

// TestAnOlderAgentIsToldNotToTrackWhenItStarts: an agent made before this
// release has none of the above until it starts, so starting one writes them
// all, whether the machine was stopped or already running.
func TestAnOlderAgentIsToldNotToTrackWhenItStarts(t *testing.T) {
	inc, files, log := doNotTrackIncus(t)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("personal", "sk-ant-oat01-personal"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ClaudeAccount: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	// As the machine was before: no DO_NOT_TRACK anywhere, and no key.
	for _, path := range []string{"/home/dev/.config/agentbox/env", "/home/dev/.config/agentbox/bash_env"} {
		old := strings.ReplaceAll(inAgent(t, files, a.Instance, path), "export DO_NOT_TRACK=1\n", "")
		if err := os.WriteFile(filepath.Join(files, a.Instance, path), []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(os.Getenv("INCUS_LOG"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	calls := log()
	if !strings.Contains(calls, "config set "+a.Instance+" environment.DO_NOT_TRACK=1") {
		t.Errorf("starting the agent didn't set the machine's environment key:\n%s", calls)
	}
	// A session that kept running (a paused machine's) gets it too.
	if !strings.Contains(calls, "tmux set-environment -g DO_NOT_TRACK 1") {
		t.Errorf("starting the agent didn't tell its running tmux server:\n%s", calls)
	}
	for _, path := range []string{"/home/dev/.config/agentbox/env", "/home/dev/.config/agentbox/bash_env"} {
		if got := inAgent(t, files, a.Instance, path); !strings.Contains(got, "export DO_NOT_TRACK=1\n") {
			t.Errorf("starting the agent didn't write DO_NOT_TRACK=1 into %s:\n%s", path, got)
		}
	}
}
