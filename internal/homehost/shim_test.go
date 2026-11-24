//go:build !windows

package homehost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A computer set up with mise, as the user whose Home chat failed with "mise
// ERROR Run with --verbose or MISE_VERBOSE=1 for more information": their tools
// are reached through mise's shims, symlinks to mise itself, and mise can
// resolve one only from the user's own configuration (~/.tool-versions, a
// global version). The chat's folder has none, so a shim run there fails the
// way mise fails.
//
// The fake mise fails as a shim, and answers `mise which` with the file of
// that name in real, when there is one.
type miseMachine struct {
	shims string // the shim directory
	mise  string // mise itself
	real  string // where mise's real tools are
}

func newMiseMachine(t *testing.T, shimmed ...string) miseMachine {
	t.Helper()
	root := t.TempDir()
	mm := miseMachine{
		shims: filepath.Join(root, "mise", "shims"),
		mise:  filepath.Join(root, "mise-bin", "mise"),
		real:  filepath.Join(root, "mise", "installs", "node", "24", "bin"),
	}
	// Only builtins: nothing here needs a PATH.
	writeExec(t, mm.mise, fmt.Sprintf(`#!/bin/sh
name=${0##*/}
if [ "$name" != mise ]; then
	echo "mise ERROR No version is set for shim: $name" >&2
	echo "mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information" >&2
	exit 1
fi
if [ "$1" = which ] && [ -x "%[1]s/$2" ]; then echo "%[1]s/$2"; exit 0; fi
echo "mise ERROR No version is set for shim: $2" >&2
exit 1
`, mm.real))
	if err := os.MkdirAll(mm.shims, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range shimmed {
		if err := os.Symlink(mm.mise, filepath.Join(mm.shims, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return mm
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func setPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

const adapterPackage = "npm:@agentclientprotocol/claude-agent-acp@1.2.3"

// claudeHost is a Host whose Claude Code is on the computer, with an adapter
// to be found or installed.
func claudeHost(t *testing.T) *Host {
	t.Helper()
	return &Host{Dir: t.TempDir(), Tools: filepath.Join(t.TempDir(), "tools"),
		Find: func() map[string]Tool { return map[string]Tool{"claude": {Path: "/opt/bin/claude", Login: true}} }}
}

// startClaude is what the helper does for the Home chat: it builds the
// adapter's command, which is run here, in the chat's folder.
func startClaude(t *testing.T, h *Host) (cmd string, output string, statuses []string, err error) {
	t.Helper()
	c, err := h.command(context.Background(), Spec{Tool: "claude", Package: adapterPackage},
		func(s string) { statuses = append(statuses, s) })
	if err != nil {
		return "", "", statuses, err
	}
	out, runErr := c.CombinedOutput()
	if runErr != nil {
		t.Fatalf("%s: %v: %s", c.Path, runErr, out)
	}
	return c.Path, string(out), statuses, nil
}

// A real adapter that says which node it was started with.
const adapterBody = "#!/bin/sh\necho \"adapter ran with node $(command -v node) in $(pwd)\"\n"

// The failure as the user hit it: claude-agent-acp on the PATH is a mise shim
// that no configuration resolves from the chat's folder. Run as it is, it says
// only "mise ERROR". mise's real adapter runs, and with mise's real node, which
// the adapter's `#!/usr/bin/env node` would otherwise find as a shim too.
func TestAdapterBehindAMiseShimRunsAsMisesRealOne(t *testing.T) {
	mm := newMiseMachine(t, "claude-agent-acp", "node", "npm")
	writeExec(t, filepath.Join(mm.real, "claude-agent-acp"), adapterBody)
	writeExec(t, filepath.Join(mm.real, "node"), "#!/bin/sh\n")
	setPath(t, mm.shims, filepath.Dir(mm.mise))

	h := claudeHost(t)
	path, out, statuses, err := startClaude(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mm.real, "claude-agent-acp"); path != want {
		t.Errorf("the adapter is %q, want mise's real %q", path, want)
	}
	if want := "adapter ran with node " + filepath.Join(mm.real, "node") + " in " + h.Dir + "\n"; out != want {
		t.Errorf("the adapter said %q, want %q", out, want)
	}
	if slices.ContainsFunc(statuses, func(s string) bool { return strings.Contains(s, "Installing") }) {
		t.Errorf("it installed an adapter although mise had one: %q", statuses)
	}
}

// Where mise can't resolve the adapter, the shim is ignored and AgentBox's own
// pinned copy is installed into Tools, with the npm and the node that run
// without mise: the ones the shims don't stand in front of.
func TestAdapterMiseCantResolveIsInstalledByARealNpm(t *testing.T) {
	mm := newMiseMachine(t, "claude-agent-acp", "node", "npm")
	system, log := t.TempDir(), filepath.Join(t.TempDir(), "npm.log")
	writeExec(t, filepath.Join(system, "node"), "#!/bin/sh\n")
	writeExec(t, filepath.Join(system, "npm"), fakeNPM(log))
	setPath(t, mm.shims, filepath.Dir(mm.mise), system, "/usr/bin", "/bin")

	h := claudeHost(t)
	path, out, statuses, err := startClaude(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(h.Tools, "node_modules", ".bin", "claude-agent-acp"); path != want {
		t.Errorf("the adapter is %q, want AgentBox's own %q", path, want)
	}
	if want := "own adapter ran with node " + filepath.Join(system, "node") + "\n"; out != want {
		t.Errorf("the adapter said %q, want %q", out, want)
	}
	if !slices.Contains(statuses, "Installing Claude Code's adapter on your computer, once") {
		t.Errorf("statuses = %q", statuses)
	}
	raw, _ := os.ReadFile(log)
	if want := "install --silent --no-fund --no-audit --prefix " + h.Tools + " @agentclientprotocol/claude-agent-acp@1.2.3\nnpm ran with node " + filepath.Join(system, "node") + "\n"; string(raw) != want {
		t.Errorf("npm ran as %q, want %q", raw, want)
	}
	if strings.Contains(out+string(raw), "mise ERROR") {
		t.Errorf("something went through mise: %q %q", out, raw)
	}
}

// npm behind a shim is resolved the same way, and installs with its own node.
func TestNpmBehindAMiseShimIsResolved(t *testing.T) {
	mm := newMiseMachine(t, "node", "npm")
	log := filepath.Join(t.TempDir(), "npm.log")
	writeExec(t, filepath.Join(mm.real, "node"), "#!/bin/sh\n")
	writeExec(t, filepath.Join(mm.real, "npm"), fakeNPM(log))
	setPath(t, mm.shims, filepath.Dir(mm.mise), "/usr/bin", "/bin")

	h := claudeHost(t)
	path, out, _, err := startClaude(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(h.Tools, "node_modules", ".bin", "claude-agent-acp"); path != want {
		t.Errorf("the adapter is %q, want AgentBox's own %q", path, want)
	}
	if want := "own adapter ran with node " + filepath.Join(mm.real, "node") + "\n"; out != want {
		t.Errorf("the adapter said %q, want %q", out, want)
	}
	raw, _ := os.ReadFile(log)
	if !strings.HasSuffix(string(raw), "npm ran with node "+filepath.Join(mm.real, "node")+"\n") {
		t.Errorf("npm ran as %q, want it with mise's real node", raw)
	}
}

// With nothing but a shim mise can't resolve, there's no node to run the
// adapter with, and the chat says what to do rather than "mise ERROR".
func TestNoNodeButAShimIsExplained(t *testing.T) {
	mm := newMiseMachine(t, "claude-agent-acp", "node", "npm")
	setPath(t, mm.shims, filepath.Dir(mm.mise))
	_, _, _, err := startClaude(t, claudeHost(t))
	if err == nil || !strings.Contains(err.Error(), "Node.js") || !strings.Contains(err.Error(), "mise use -g node") || strings.Contains(err.Error(), "mise ERROR") {
		t.Fatalf("err = %v", err)
	}
	setPath(t, t.TempDir())
	_, _, _, err = startClaude(t, claudeHost(t))
	if err == nil || !strings.Contains(err.Error(), "Node.js, which isn't installed") {
		t.Fatalf("err = %v", err)
	}
}

// An adapter that is a real executable runs as it did, with node still first
// on its PATH.
func TestARealAdapterRunsAsItIs(t *testing.T) {
	bin := t.TempDir()
	writeExec(t, filepath.Join(bin, "claude-agent-acp"), adapterBody)
	writeExec(t, filepath.Join(bin, "node"), "#!/bin/sh\n")
	setPath(t, bin, "/usr/bin", "/bin")
	path, out, _, err := startClaude(t, claudeHost(t))
	if err != nil || path != filepath.Join(bin, "claude-agent-acp") || !strings.HasPrefix(out, "adapter ran with node "+filepath.Join(bin, "node")+" in ") {
		t.Fatalf("%q ran as %q, %v", path, out, err)
	}
}

// The AI tools themselves are found the same way: by the real path mise gives,
// or not at all when no configuration says what a shim runs.
func TestFindToolsResolvesShims(t *testing.T) {
	mm := newMiseMachine(t, "claude", "codex")
	writeExec(t, filepath.Join(mm.real, "claude"), "#!/bin/sh\n")
	bin := t.TempDir()
	writeExec(t, filepath.Join(bin, "opencode"), "#!/bin/sh\n")
	t.Setenv("HOME", t.TempDir())
	setPath(t, mm.shims, filepath.Dir(mm.mise), bin)

	tools := FindTools()
	if want := filepath.Join(mm.real, "claude"); tools["claude"].Path != want {
		t.Errorf("claude = %q, want mise's real %q", tools["claude"].Path, want)
	}
	if tools["codex"].Path != "" {
		t.Errorf("codex = %q: the shim fails here, so it isn't installed", tools["codex"].Path)
	}
	if want := filepath.Join(bin, "opencode"); tools["opencode"].Path != want {
		t.Errorf("opencode = %q, want %q", tools["opencode"].Path, want)
	}
}

func TestPathFirst(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := pathFirst([]string{"A=1", "PATH=/a" + sep + "/b", "B=2"}, "/node")
	if got[len(got)-1] != "PATH=/node"+sep+"/a"+sep+"/b" {
		t.Errorf("env = %q", got)
	}
	if got := pathFirst([]string{"A=1"}, "/node"); !slices.Equal(got, []string{"A=1", "PATH=/node"}) {
		t.Errorf("env = %q", got)
	}
	// The one a later entry overrides is the one that counts.
	got = pathFirst([]string{"PATH=/old", "PATH=/new"}, "/node")
	if got[len(got)-1] != "PATH=/node"+sep+"/new" {
		t.Errorf("env = %q", got)
	}
}

// fakeNPM installs the adapter the way the real one lays it out, and says
// what it was run with and with which node.
func fakeNPM(log string) string {
	return fmt.Sprintf(`#!/bin/sh
echo "$*" > %[1]q
echo "npm ran with node $(command -v node)" >> %[1]q
while [ "$1" != --prefix ]; do shift; done
prefix=$2
mkdir -p "$prefix/node_modules/.bin" "$prefix/node_modules/@agentclientprotocol/claude-agent-acp"
printf '#!/bin/sh\necho "own adapter ran with node $(command -v node)"\n' > "$prefix/node_modules/.bin/claude-agent-acp"
chmod +x "$prefix/node_modules/.bin/claude-agent-acp"
printf '{"version":"1.2.3"}' > "$prefix/node_modules/@agentclientprotocol/claude-agent-acp/package.json"
`, log)
}
