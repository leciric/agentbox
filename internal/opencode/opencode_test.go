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

// Models runs the real command line, so a fake one on PATH is what this can
// check: that the login AgentBox owns is the one OpenCode is asked about, and
// that a failure is an ordinary error rather than an empty list.
func TestModelsUsesAgentBoxsOwnDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fake command line is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s/from-%s\\n' opencode \"$XDG_DATA_HOME\"\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dataHome := t.TempDir()
	got, err := opencode.Models(context.Background(), dataHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0], dataHome) {
		t.Errorf("Models() = %v, want one id naming %s", got, dataHome)
	}

	// A command line that fails says so, and names what it printed.
	failing := "#!/bin/sh\necho 'no providers are configured' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(failing), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := opencode.Models(context.Background(), dataHome); err == nil || !strings.Contains(err.Error(), "no providers are configured") {
		t.Errorf("a failing opencode models: %v", err)
	}
}

func TestModelsWithoutOpenCodeInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := opencode.Models(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Errorf("without opencode on PATH: %v", err)
	}
}
