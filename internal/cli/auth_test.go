package cli_test

import (
	"strings"
	"testing"
)

func TestAuthSaysHowToInstallAMissingTool(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())

	_, err := run(t, "", "auth", "codex")
	if err == nil || !strings.Contains(err.Error(), "the Codex CLI on this machine, and it isn't installed") || !strings.Contains(err.Error(), "npm install -g @openai/codex") {
		t.Errorf("auth codex without codex: err = %v", err)
	}

	_, err = run(t, "", "auth", "opencode")
	if err == nil || !strings.Contains(err.Error(), "the OpenCode CLI on this machine, and it isn't installed") || !strings.Contains(err.Error(), "npm install -g opencode-ai") {
		t.Errorf("auth opencode without opencode: err = %v", err)
	}
}
