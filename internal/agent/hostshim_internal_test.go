package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"agentbox/internal/paths"
)

// A machine set up with mise, as the user whose Home chat failed with "mise
// ERROR Run with --verbose or MISE_VERBOSE=1 for more information": the tools
// are reached through mise's shims, symlinks to mise itself, and mise can only
// resolve them from the user's own HOME and directories (~/.tool-versions,
// ~/.mise.toml, a trusted project config). The lead has a private HOME, so for
// it every shim fails the way mise fails there.
type miseMachine struct {
	shims string // ~/.local/share/mise/shims
	mise  string // mise itself
	real  string // where mise installed the real tools, when `mise which` can say
}

// newMiseMachine makes the shims for tools, with a mise that fails as a shim
// and answers `mise which` only when resolves is true.
func newMiseMachine(t *testing.T, resolves bool, tools ...string) miseMachine {
	t.Helper()
	root := t.TempDir()
	mm := miseMachine{
		shims: filepath.Join(root, "mise", "shims"),
		mise:  filepath.Join(root, "mise-bin", "mise"),
		real:  filepath.Join(root, "mise", "installs", "node", "24", "bin"),
	}
	which := `echo "mise ERROR No version is set for shim: $2" >&2; exit 1`
	if resolves {
		which = fmt.Sprintf(`echo %s/"$2"`, mm.real)
	}
	writeExec(t, mm.mise, fmt.Sprintf(`#!/bin/sh
name=$(basename "$0")
if [ "$name" != mise ]; then
	echo "mise ERROR No version is set for shim: $name" >&2
	echo "mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information" >&2
	exit 1
fi
if [ "$1" = which ]; then %s; exit 0; fi
exit 1
`, which))
	if err := os.MkdirAll(mm.shims, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if err := os.Symlink(mm.mise, filepath.Join(mm.shims, tool)); err != nil {
			t.Fatal(err)
		}
		writeExec(t, filepath.Join(mm.real, tool), "#!/bin/sh\necho \"mise's real "+tool+" ran $*\"\n")
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

// stubHostBasics gives m its own Claude Code and gh, so hostTools has only
// Node and the adapter left to find.
func stubHostBasics(t *testing.T, m *Manager) {
	t.Helper()
	writeExec(t, m.claudePath(), "#!/bin/sh\nexit 0\n")
	writeExec(t, m.ghPath(), "#!/bin/sh\nexit 0\n")
}

// fakeNode writes a node that prints what it was asked to run, and an npm that
// installs the adapter the way the real one lays it out, into dir/bin.
func fakeNode(t *testing.T, dir string) {
	t.Helper()
	writeExec(t, filepath.Join(dir, "bin", "node"), "#!/bin/sh\necho \"AgentBox's node ran $*\"\n")
	pkg := strings.TrimPrefix(ChatAdapters["claude"].Package, "npm:")
	name := pkg[:strings.LastIndex(pkg, "@")]
	writeExec(t, filepath.Join(dir, "bin", "npm"), fmt.Sprintf(`#!/bin/sh
while [ "$1" != --prefix ]; do shift; done
prefix=$2
mkdir -p "$prefix/node_modules/.bin" "$prefix/node_modules/%[1]s"
printf '#!/usr/bin/env node\n' > "$prefix/node_modules/.bin/%[2]s"
chmod +x "$prefix/node_modules/.bin/%[2]s"
printf '{"version":"%[3]s"}' > "$prefix/node_modules/%[1]s/package.json"
`, name, ChatAdapters["claude"].Command, pinnedVersion(pkg)))
}

// nodeDist serves a Node release tarball holding fakeNode, as nodejs.org/dist
// lays it out, and counts the downloads.
func nodeDist(t *testing.T) (url string, downloads *atomic.Int32) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "node-"+NodeVersion)
	fakeNode(t, src)
	tarball := filepath.Join(t.TempDir(), "node.tar.xz")
	if out, err := exec.Command("tar", "-cJf", tarball, "-C", filepath.Dir(src), filepath.Base(src)).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	downloads = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != fmt.Sprintf("/%[1]s/node-%[1]s-linux-%[2]s.tar.xz", NodeVersion, nodeArch()) {
			http.NotFound(w, r)
			return
		}
		downloads.Add(1)
		http.ServeFile(w, r, tarball)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, downloads
}

// runAsLead runs the adapter the way LeadChatCommand does: with the lead's
// private HOME, and its PATH of the tools' directories then the daemon's.
func runAsLead(t *testing.T, tools hostTools) (string, error) {
	t.Helper()
	command, args := tools.command()
	cmd := exec.Command(command, args...)
	cmd.Env = []string{
		"HOME=" + t.TempDir(),
		"PATH=" + tools.Bin + string(os.PathListSeparator) + withoutShims(os.Getenv("PATH")),
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The failure as the user hit it: their own claude-agent-acp on the PATH, a
// real npm install whose `#!/usr/bin/env node` finds node only as a mise
// shim, and a daemon started with the shims on its PATH but not mise itself.
// The chat used to run that adapter directly, so its node was the shim, and
// the shim, with the lead's HOME, said only "mise ERROR". It must run with
// mise's real node, found through the shim's link to mise.
func TestTheLeadsAdapterNeverRunsThroughANodeShim(t *testing.T) {
	mm := newMiseMachine(t, true, "node", "npm")
	userBin := t.TempDir()
	adapter := filepath.Join(userBin, ChatAdapters["claude"].Command)
	writeExec(t, adapter, "#!/usr/bin/env node\nconsole.log('adapter')\n")
	t.Setenv("PATH", strings.Join([]string{userBin, mm.shims, "/usr/bin", "/bin"}, string(os.PathListSeparator)))

	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, nodeDist: "http://127.0.0.1:1"}
	stubHostBasics(t, m)
	tools, err := m.hostTools(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if tools.Adapter != adapter {
		t.Errorf("Adapter = %q, want the user's real one %q", tools.Adapter, adapter)
	}
	out, err := runAsLead(t, tools)
	if err != nil || !strings.Contains(out, "mise's real node ran "+adapter) {
		t.Errorf("the adapter ran as %q (%v), want mise's real node running %s", out, err, adapter)
	}
	for _, dir := range filepath.SplitList(tools.Bin) {
		if isShim(filepath.Join(dir, "x")) {
			t.Errorf("the lead's PATH additions %q include the shims %s", tools.Bin, dir)
		}
	}
}

// When mise can't name a node from here either (no global version, an
// untrusted config), the lead gets AgentBox's own Node rather than the shim.
func TestTheLeadGetsAgentBoxsNodeWhenMiseCantResolveOne(t *testing.T) {
	mm := newMiseMachine(t, false, "node", "npm")
	userBin := t.TempDir()
	adapter := filepath.Join(userBin, ChatAdapters["claude"].Command)
	writeExec(t, adapter, "#!/usr/bin/env node\nconsole.log('adapter')\n")
	t.Setenv("PATH", strings.Join([]string{userBin, mm.shims, filepath.Dir(mm.mise), "/usr/bin", "/bin"}, string(os.PathListSeparator)))

	dist, downloads := nodeDist(t)
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, nodeDist: dist}
	stubHostBasics(t, m)
	tools, err := m.hostTools(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Errorf("Node was downloaded %d times, want once", downloads.Load())
	}
	out, err := runAsLead(t, tools)
	if err != nil || !strings.Contains(out, "AgentBox's node ran "+adapter) {
		t.Errorf("the adapter ran as %q (%v), want AgentBox's node running %s", out, err, adapter)
	}
	if strings.Contains(out, "mise ERROR") {
		t.Errorf("the adapter went through mise: %q", out)
	}
}

// When mise can name the real node, the machine's own is used and nothing is
// downloaded, even with mise itself off the daemon's PATH: a shim is a symlink
// to mise, which is how it is found.
func TestTheLeadsNodeIsMisesRealOneWhenMiseCanSay(t *testing.T) {
	mm := newMiseMachine(t, true, "node", "npm")
	t.Setenv("PATH", strings.Join([]string{mm.shims, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	if got, want := onPath("node"), filepath.Join(mm.real, "node"); got != want {
		t.Errorf("onPath(node) = %q, want mise's real %q", got, want)
	}
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, nodeDist: "http://127.0.0.1:1"}
	nodeBin, err := m.ensureNode(context.Background(), func(s string) { t.Errorf("ensureNode reported %q: it had nothing to install", s) })
	if err != nil || nodeBin != mm.real {
		t.Errorf("ensureNode() = %q, %v, want mise's %q", nodeBin, err, mm.real)
	}
}

// An adapter the PATH reaches only through a shim is passed over: mise's
// answer for it depends on a HOME and directory the lead doesn't share.
// AgentBox installs its own and runs that with a real node.
func TestAnAdapterBehindAShimIsReplacedByAgentBoxsOwn(t *testing.T) {
	mm := newMiseMachine(t, true, ChatAdapters["claude"].Command)
	t.Setenv("PATH", strings.Join([]string{mm.shims, filepath.Dir(mm.mise), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	stubHostBasics(t, m)
	fakeNode(t, m.nodeDir())
	if m.LeadToolInstalled(context.Background(), "claude") {
		t.Error("LeadToolInstalled says the adapter is there, but the PATH has it only as a shim")
	}
	tools, err := m.hostTools(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if tools.Adapter != m.adapterPath() {
		t.Errorf("Adapter = %q, want AgentBox's own %q", tools.Adapter, m.adapterPath())
	}
	out, err := runAsLead(t, tools)
	if err != nil || !strings.Contains(out, "AgentBox's node ran "+m.adapterPath()) {
		t.Errorf("the adapter ran as %q (%v), want AgentBox's node running AgentBox's adapter", out, err)
	}
}

func TestWithoutShimsDropsOnlyShimDirectories(t *testing.T) {
	t.Parallel()
	sep := string(os.PathListSeparator)
	path := strings.Join([]string{"/home/you/.local/share/mise/shims", "/usr/bin", "/opt/asdf/shims", "", "/home/you/.local/bin", "/home/you/shims-not"}, sep)
	got := filepath.SplitList(withoutShims(path))
	want := []string{"/usr/bin", "/home/you/.local/bin", "/home/you/shims-not"}
	if !slices.Equal(got, want) {
		t.Errorf("withoutShims() = %q, want %q", got, want)
	}
}

// A Node script is handed to the chosen node; anything else runs as itself.
func TestHostToolsCommandRunsNodeScriptsWithItsNode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script, binary := filepath.Join(dir, "script"), filepath.Join(dir, "binary")
	writeExec(t, script, "#!/usr/bin/env node\n")
	writeExec(t, binary, "#!/bin/sh\n")
	if cmd, args := (hostTools{Adapter: script, Node: "/n/node"}).command(); cmd != "/n/node" || !slices.Equal(args, []string{script}) {
		t.Errorf("command() for a Node script = %q %q", cmd, args)
	}
	if cmd, args := (hostTools{Adapter: binary, Node: "/n/node"}).command(); cmd != binary || len(args) != 0 {
		t.Errorf("command() for a non-Node adapter = %q %q", cmd, args)
	}
}
