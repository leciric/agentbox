package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/credentials"
	"agentbox/internal/opencode"
)

func TestAuthSaysHowToInstallAMissingTool(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())

	_, err := run(t, "", "auth", "codex")
	if err == nil || !strings.Contains(err.Error(), "the Codex CLI on this machine, and it isn't installed") || !strings.Contains(err.Error(), "npm install -g @openai/codex") {
		t.Errorf("auth codex without codex: err = %v", err)
	}

}

// The VM `agentbox auth opencode` runs in has no OpenCode on PATH, which used
// to end the login before it began. It runs AgentBox's own pinned copy, the
// one opencode.Installer downloads into the tools directory (faked here, as a
// download leaves it), against AgentBox's own data directory.
func TestAuthOpenCodeRunsAgentBoxsOwnOpenCode(t *testing.T) {
	home := isolate(t)
	t.Setenv("PATH", t.TempDir())
	own := opencode.Installer{Dir: filepath.Join(home, ".local", "share", "agentbox", "tools")}.Path()
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$@\" >\"${0%/*}/args\"\necho \"$XDG_CONFIG_HOME\" >\"${0%/*}/confighome\"\nprintf '{\"anthropic\":{\"type\":\"api\",\"key\":\"k\"}}' >\"$XDG_DATA_HOME/opencode/auth.json\"\n"
	if err := os.WriteFile(own, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "opencode", "--provider", "anthropic", "--account", "work")
	if err != nil {
		t.Fatalf("auth opencode: %v\n%s", err, out)
	}
	if args, _ := os.ReadFile(filepath.Join(filepath.Dir(own), "args")); strings.TrimSpace(string(args)) != "auth login --provider anthropic" {
		t.Errorf("ran opencode %q", args)
	}
	creds := credentials.Store{Dir: filepath.Join(home, ".config", "agentbox", "credentials")}
	// The login must not read the host's opencode.json: this test's XDG home
	// has one, and a config written for the other release is one this OpenCode
	// refuses. It reads an empty config directory of AgentBox's instead.
	configHome, _ := os.ReadFile(filepath.Join(filepath.Dir(own), "confighome"))
	if got := strings.TrimSpace(string(configHome)); !strings.HasPrefix(got, creds.Dir) {
		t.Errorf("the login's XDG_CONFIG_HOME = %q, want one under %s", got, creds.Dir)
	}
	// Kept as the provider's account of that name, and nothing else.
	if ok, _ := creds.HasToolAccount("opencode", "anthropic", "work"); !ok || !creds.HasOpenCodeLogin(nil) {
		t.Errorf("no anthropic account \"work\" after: %s", out)
	}
	if entries, _ := os.ReadDir(creds.Dir); len(entries) != 1 {
		t.Errorf("the login's scratch directory was left behind: %v", entries)
	}
}
