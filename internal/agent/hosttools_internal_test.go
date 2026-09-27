package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/paths"
)

// A project's chat runs with its own HOME, so anything it runs must be a real
// executable. A version manager's shim is the manager itself, working out what
// to run from directories under HOME, so a shim in the lead's environment fails
// with "not a valid shim" and takes the adapter down with it. Found on a real
// machine after 0.3.0, which is why each of these is pinned.
func TestIsShim(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/home/you/.local/share/mise/shims/claude-agent-acp":        true,
		"/home/you/.local/share/mise/shims/node":                    true,
		"/opt/asdf/shims/node":                                      true,
		"/home/you/.local/share/mise/installs/claude/latest/claude": false,
		"/home/you/.local/share/agentbox/tools/.local/bin/claude":   false,
		"/usr/bin/node":                       false,
		"/home/you/shims-are-not-here/claude": false,
		"":                                    false,
	} {
		if got := isShim(path); got != want {
			t.Errorf("isShim(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDeshimLeavesRealBinariesAlone(t *testing.T) {
	t.Parallel()
	real := "/home/you/.local/share/mise/installs/claude/latest/claude"
	if got := deshim(real); got != real {
		t.Errorf("deshim(%q) = %q, want it unchanged", real, got)
	}
	if got := deshim(""); got != "" {
		t.Errorf("deshim(\"\") = %q", got)
	}
}

// A shim that can't be resolved is not a tool the lead can run, so it reports
// nothing and the caller installs AgentBox's own copy instead. Reporting the
// shim would hand the lead something that fails the moment it starts.
func TestDeshimReportsNothingWhenItCannotResolve(t *testing.T) {
	// A PATH with no version manager on it.
	t.Setenv("PATH", t.TempDir())
	if got := deshim("/home/you/.local/share/mise/shims/claude-agent-acp"); got != "" {
		t.Errorf("deshim() = %q, want nothing: a shim it can't resolve is unusable", got)
	}
	if got := onPath("claude-agent-acp"); got != "" {
		t.Errorf("onPath() = %q, want nothing", got)
	}
}

// onPath finds a real binary, and is what the lead's tools are resolved with.
func TestOnPathFindsARealBinary(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "a-real-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := onPath("a-real-tool"); got != tool {
		t.Errorf("onPath() = %q, want %q", got, tool)
	}
}

// An installer is run by the daemon, which a desktop launcher may have started
// with almost nothing on its PATH. Without the system directories, Claude
// Code's installer fails on a missing `tr` and says nothing useful about why.
func TestInstallEnvAlwaysHasTheSystemDirectories(t *testing.T) {
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	t.Setenv("PATH", "/only/this")
	env := map[string]string{}
	for _, kv := range m.installEnv() {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env[name] = value // later entries win, as they do for a process
		}
	}
	for _, dir := range []string{"/usr/bin", "/bin", "/usr/local/bin"} {
		if !strings.Contains(env["PATH"], dir) {
			t.Errorf("PATH = %q, want %s on it", env["PATH"], dir)
		}
	}
	if !strings.Contains(env["PATH"], "/only/this") {
		t.Errorf("PATH = %q, want the daemon's own entries kept too", env["PATH"])
	}
	// Nothing an installer writes may land in the user's home.
	if env["HOME"] != m.toolsHome() {
		t.Errorf("HOME = %q, want AgentBox's tools directory %q", env["HOME"], m.toolsHome())
	}
}

func TestNodeArchIsOneNodePublishes(t *testing.T) {
	t.Parallel()
	if arch := nodeArch(); arch != "x64" && arch != "arm64" {
		t.Errorf("nodeArch() = %q, which nodejs.org has no tarball for", arch)
	}
}

// The lead's adapter carries its own Claude Code, so one an older AgentBox
// installed has to be told apart from the pinned one, or the chat stays on
// that Claude Code forever.
func TestAdapterVersionReadsWhatIsInstalled(t *testing.T) {
	t.Parallel()
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	if v := m.adapterVersion(); v != "" {
		t.Fatalf("adapterVersion() with nothing installed = %q, want \"\"", v)
	}
	dir := filepath.Join(m.toolsHome(), "node_modules", "@agentclientprotocol", "claude-agent-acp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"@agentclientprotocol/claude-agent-acp","version":"0.76.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := m.adapterVersion(); v != "0.76.0" {
		t.Errorf("adapterVersion() = %q, want 0.76.0", v)
	}
	if pinned := pinnedVersion(ChatAdapters["claude"].Package); pinned == "0.76.0" || pinned == "" {
		t.Errorf("pinnedVersion() = %q, want the version the pin names", pinned)
	}
}
