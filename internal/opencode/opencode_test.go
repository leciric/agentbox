package opencode_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/opencode"
)

func TestParseModels(t *testing.T) {
	out := `opencode/big-pickle
github-copilot/claude-opus-5
anthropic/claude-sonnet-5

anthropic/claude-sonnet-5
Warning: provider zed is not configured
not-a-model
/leading-slash
trailing-slash/
`
	got := opencode.ParseModels(out)
	want := []string{"opencode/big-pickle", "github-copilot/claude-opus-5", "anthropic/claude-sonnet-5"}
	if !slices.Equal(got, want) {
		t.Errorf("ParseModels() = %v, want %v", got, want)
	}
	if got := opencode.ParseModels(""); len(got) != 0 {
		t.Errorf("nothing printed = %v, want no models", got)
	}
}

func TestName(t *testing.T) {
	if got := opencode.Name("github-copilot/gpt-5.4"); got != "github-copilot / gpt-5.4" {
		t.Errorf("Name() = %q", got)
	}
	// Nothing is invented for something that isn't an id.
	if got := opencode.Name("sonnet"); got != "sonnet" {
		t.Errorf("Name() = %q", got)
	}
}

// Models runs a real command line, so a fake one is what this can check: that the login AgentBox owns is the one OpenCode is asked about, and
// that a failure is an ordinary error rather than an empty list.
func TestModelsUsesAgentBoxsOwnDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fake command line is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$XDG_CONFIG_HOME\" >\"${0%/*}/confighome\"\nprintf '%s/from-%s\\n' opencode \"$XDG_DATA_HOME\"\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(bin, "opencode")

	dataHome := t.TempDir()
	got, err := opencode.Models(context.Background(), command, dataHome, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0], dataHome) {
		t.Errorf("Models() = %v, want one id naming %s", got, dataHome)
	}
	// And it reads AgentBox's config, not the host's, which may be written for
	// the other release and which this one refuses.
	config, err := os.ReadFile(filepath.Join(bin, "confighome"))
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != filepath.Join(dataHome, "config") {
		t.Errorf("XDG_CONFIG_HOME = %q, want %q", config, filepath.Join(dataHome, "config"))
	}

	// A command line that fails says so, and names what it printed.
	failing := "#!/bin/sh\necho 'no providers are configured' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(failing), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := opencode.Models(context.Background(), command, dataHome, false); err == nil || !strings.Contains(err.Error(), "no providers are configured") {
		t.Errorf("a failing opencode models: %v", err)
	}
}

// OpenCode 2 is asked in standalone mode, and its credentials go in and out
// through its own commands rather than an auth.json.
func TestVersionTwoTalksToAStandaloneServer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fake command line is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n  models) printf '%s\\n' \"$@\" ;;\n  auth) cat >\"$XDG_DATA_HOME/stdin\"; printf '%s\\n' \"$@\" ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode2"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(bin, "opencode2")
	dataHome := t.TempDir()

	if err := opencode.Import(context.Background(), command, dataHome, []byte(`[{"id":"cred_1"}]`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dataHome, "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[{"id":"cred_1"}]` {
		t.Errorf("auth import read %q", got)
	}
}

// HostEnv leaves OpenCode no way back to the host's own configuration,
// database or credentials, and points it at AgentBox's instead.
func TestHostEnvIsolatesTheHostsOpenCode(t *testing.T) {
	env := opencode.HostEnv("/agentbox/opencode", []string{
		"HOME=/home/ana",
		"PATH=/usr/bin",
		"XDG_DATA_HOME=/home/ana/.local/share",
		"XDG_CONFIG_HOME=/home/ana/.config",
		"OPENCODE_CONFIG=/home/ana/.config/opencode/opencode.json",
		"OPENCODE_CONFIG_CONTENT={}",
		"OPENCODE_CONFIG_DIR=/home/ana/.config/opencode",
		"OPENCODE_AUTH_CONTENT={}",
		"OPENCODE_DB=/home/ana/.local/share/opencode/opencode.db",
	})
	got := map[string]string{}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if _, dup := got[name]; dup {
			t.Errorf("%s appears twice", name)
		}
		got[name] = value
	}
	want := map[string]string{
		"HOME":            "/home/ana",
		"PATH":            "/usr/bin",
		"XDG_DATA_HOME":   "/agentbox/opencode",
		"XDG_CONFIG_HOME": filepath.Join("/agentbox/opencode", "config"),
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	for _, name := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CONFIG_DIR", "OPENCODE_AUTH_CONTENT", "OPENCODE_DB"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s survived HostEnv", name)
		}
	}
}
