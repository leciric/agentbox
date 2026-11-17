package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The lead runs its AI tool on the host, so Claude Code and its ACP adapter
// have to be there (and the GitHub CLI, hostgh.go). They are not bundled in the app: the binary alone is
// larger than the AppImage, and a bundled copy would go stale. They are
// installed on first use instead, into AgentBox's own directory, reported
// through the chat's status line the same way an agent's adapter is.
//
// Nothing goes on the user's PATH, and nothing touches the host's own
// ~/.claude: the lead's login comes from AgentBox's stored account (D6).

// hostInstallTimeout bounds the first-use download. Claude Code is ~214 MiB.
const hostInstallTimeout = 15 * time.Minute

// hostTools are the paths the lead's adapter runs with.
type hostTools struct {
	Adapter string // claude-agent-acp: AgentBox's own, or a real one from the PATH
	Node    string // the real node it runs with, never a version manager's shim
	Bin     string // directories holding the tools, prepended to PATH
}

// command is what starts the adapter. A Node script is handed to node by
// path rather than left to its `#!/usr/bin/env node`, which picks whichever
// node comes first on the PATH, and on a machine with mise that is a shim:
// mise itself, which under the lead's private HOME can't see the user's
// ~/.tool-versions or ~/.mise.toml and fails with "mise ERROR".
func (t hostTools) command() (string, []string) {
	if t.Node != "" && nodeScript(t.Adapter) {
		return t.Node, []string{t.Adapter}
	}
	return t.Adapter, nil
}

// nodeScript reports whether path is a script whose #! line runs node.
func nodeScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 128)
	n, _ := f.Read(head)
	line, _, _ := strings.Cut(string(head[:n]), "\n")
	return strings.HasPrefix(line, "#!") && strings.Contains(line, "node")
}

// toolsHome is a HOME for the installers, so Claude Code's own installer puts
// its binary under AgentBox rather than in the user's ~/.local/bin.
func (m *Manager) toolsHome() string { return m.Paths.Tools() }

func (m *Manager) claudePath() string {
	return filepath.Join(m.toolsHome(), ".local", "bin", "claude")
}

func (m *Manager) adapterPath() string {
	return filepath.Join(m.toolsHome(), "node_modules", ".bin", ChatAdapters["claude"].Command)
}

// hostTools finds Claude Code, its adapter and the GitHub CLI, installing
// whichever is missing.
// status reports what it is doing, which the chat shows while it starts.
func (m *Manager) hostTools(ctx context.Context, status func(detail string)) (hostTools, error) {
	ctx, cancel := context.WithTimeout(ctx, hostInstallTimeout)
	defer cancel()

	claude, err := m.ensureClaude(ctx, status)
	if err != nil {
		return hostTools{}, err
	}
	adapter, err := m.ensureAdapter(ctx, status)
	if err != nil {
		return hostTools{}, err
	}
	// The adapter is a Node script, so it needs a node that runs without a
	// version manager: the machine's own resolved past any shim, else
	// AgentBox's. It goes on the PATH ahead of the daemon's too, for whatever
	// the adapter starts with `node` or `npx`.
	nodeBin, err := m.ensureNode(ctx, status)
	if err != nil {
		return hostTools{}, err
	}
	bin := []string{filepath.Dir(claude), nodeBin, filepath.Dir(adapter)}
	// The chat works without gh, so a failed install costs the lead only gh.
	if gh, err := m.ensureGH(ctx, status); err != nil {
		m.logf("The project chat starts without the GitHub CLI: %v", err)
	} else {
		bin = append(bin, filepath.Dir(gh))
	}
	out := hostTools{Adapter: adapter, Node: filepath.Join(nodeBin, "node"), Bin: strings.Join(unique(bin), string(os.PathListSeparator))}
	m.logf("The project chat runs %s with %s, with %s on its PATH", out.Adapter, out.Node, out.Bin)
	return out, nil
}

// withoutShims is a PATH with every version manager's shim directory taken
// out. The lead inherits the daemon's PATH, and a shim there would be found
// before the real tool by anything the adapter starts, then fail for want of
// the user's HOME.
func withoutShims(path string) string {
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if dir != "" && !isShim(filepath.Join(dir, "x")) {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// deshim turns a version manager's shim into the real executable. A shim is the
// manager itself, deciding what to run from directories under HOME; the lead
// has its own HOME, so a shim there finds nothing. mise answers `mise which`
// with the real path, and that path needs no manager to run.
func deshim(path string) string {
	if !isShim(path) {
		return path
	}
	mise := findMise(path)
	if mise == "" {
		return ""
	}
	out, err := exec.Command(mise, "which", filepath.Base(path)).Output()
	real := strings.TrimSpace(string(out))
	if err != nil || real == "" || isShim(real) {
		// mise can't resolve it from here either, so this isn't a tool the lead
		// can run. The caller installs AgentBox's own copy instead.
		return ""
	}
	return real
}

// findMise is the mise that answers for a shim: the PATH's, else the one the
// shim links to. A desktop launcher can start the daemon with the shims on its
// PATH but not mise itself, and mise's shims are symlinks to it.
func findMise(shim string) string {
	if mise, err := exec.LookPath("mise"); err == nil {
		return mise
	}
	if real, err := filepath.EvalSymlinks(shim); err == nil && strings.TrimSuffix(filepath.Base(real), ".exe") == "mise" && usable(real) {
		return real
	}
	return ""
}

func isShim(path string) bool {
	return path != "" && strings.Contains(path, string(os.PathSeparator)+"shims"+string(os.PathSeparator))
}

// onPath finds a command the lead can run: on the PATH, and resolved past any
// shim, because a shim needs the HOME the lead doesn't have.
func onPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return deshim(path)
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// ensureClaude returns the path of the Claude Code binary, downloading it the
// first time. AgentBox's own copy wins over one on the PATH, so an agent's
// chat and a project's chat don't drift apart.
func (m *Manager) ensureClaude(ctx context.Context, status func(string)) (string, error) {
	own := m.claudePath()
	if usable(own) {
		return own, nil
	}
	if path := onPath(ChatTool); path != "" {
		return path, nil
	}
	status("Downloading Claude Code (about 214 MiB), once for this machine")
	if err := os.MkdirAll(m.toolsHome(), 0o700); err != nil {
		return "", err
	}
	// The installer is a shell script that respects HOME, so the binary lands
	// in AgentBox's directory and never on the user's PATH.
	script := fmt.Sprintf("set -e\ncurl -fsSL %s | bash -s %s", installerURL, ChatToolVersion)
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = m.installEnv()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", installError("Claude Code", err, out.String(), ctx)
	}
	if !usable(own) {
		return "", fmt.Errorf("installing Claude Code: it reported success but %s isn't there: %s", own, tail(out.String()))
	}
	return own, nil
}

// NodeVersion is the Node AgentBox installs for itself when the machine has
// none. The ACP adapter is an npm package, so without Node there is no chat.
const NodeVersion = "v24.21.0"

func (m *Manager) nodeDir() string { return filepath.Join(m.toolsHome(), "node") }

// ensureNode returns the directory holding node and npm, installing Node into
// AgentBox's own tools directory when the machine has none of its own. It never
// goes on the user's PATH.
func (m *Manager) ensureNode(ctx context.Context, status func(string)) (string, error) {
	own := filepath.Join(m.nodeDir(), "bin")
	if usable(filepath.Join(own, "npm")) && usable(filepath.Join(own, "node")) {
		return own, nil
	}
	// The machine's own, when node and npm are side by side, resolved past any
	// shim: callers run <dir>/node and <dir>/npm.
	if npm := onPath("npm"); npm != "" {
		if dir := filepath.Dir(npm); usable(filepath.Join(dir, "node")) && !isShim(filepath.Join(dir, "node")) {
			return dir, nil
		}
	}
	if node := onPath("node"); node != "" {
		if dir := filepath.Dir(node); usable(filepath.Join(dir, "npm")) {
			return dir, nil
		}
	}
	status("Installing Node, once for this machine")
	if err := os.MkdirAll(m.nodeDir(), 0o700); err != nil {
		return "", err
	}
	// One tarball, extracted with its top directory stripped, so node and npm
	// land in <tools>/node/bin.
	dist := cmp.Or(m.nodeDist, "https://nodejs.org/dist")
	url := fmt.Sprintf("%[3]s/%[1]s/node-%[1]s-linux-%[2]s.tar.xz", NodeVersion, nodeArch(), dist)
	script := fmt.Sprintf("set -e\ncurl -fsSL %s | tar -xJ --strip-components=1 -C %s", shellQuote(url), shellQuote(m.nodeDir()))
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = m.installEnv()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", installError("Node "+NodeVersion, err, out.String(), ctx)
	}
	if !usable(filepath.Join(own, "npm")) {
		return "", fmt.Errorf("installing Node: it reported success but %s isn't there: %s", filepath.Join(own, "npm"), tail(out.String()))
	}
	return own, nil
}

// nodeArch is Node's name for this machine's architecture.
func nodeArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "x64"
}

// ensureAdapter returns the path of the ACP adapter, installing it with npm the
// first time. Unlike Claude Code it is an npm package, so it needs Node, which
// ensureNode provides when the machine has none.
func (m *Manager) ensureAdapter(ctx context.Context, status func(string)) (string, error) {
	adapter := ChatAdapters["claude"]
	own := m.adapterPath()
	// The adapter carries its own Claude Code, so its version is the chat's:
	// one installed by an older AgentBox is replaced rather than kept forever.
	installed := m.adapterVersion()
	if usable(own) && installed == pinnedVersion(adapter.Package) {
		return own, nil
	}
	if path := pathAdapter(); path != "" && installed == "" {
		return path, nil
	}
	nodeBin, err := m.ensureNode(ctx, status)
	if err != nil {
		return "", err
	}
	if installed == "" {
		status("Installing the Claude Code adapter, once for this machine")
	} else {
		status(fmt.Sprintf("Updating the Claude Code adapter from %s to %s", installed, pinnedVersion(adapter.Package)))
	}
	if err := os.MkdirAll(m.toolsHome(), 0o700); err != nil {
		return "", err
	}
	pkg := strings.TrimPrefix(adapter.Package, "npm:")
	cmd := exec.CommandContext(ctx, filepath.Join(nodeBin, "npm"), "install", "--silent", "--no-fund", "--no-audit", "--prefix", m.toolsHome(), pkg)
	cmd.Env = m.installEnv()
	// npm runs node, and the only node this machine is sure to have is the one
	// next to it.
	cmd.Env = append(cmd.Env, "PATH="+nodeBin+string(os.PathListSeparator)+os.Getenv("PATH")+string(os.PathListSeparator)+systemPath)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", installError(pkg, err, out.String(), ctx)
	}
	if !usable(own) {
		return "", fmt.Errorf("installing %s: it reported success but %s isn't there: %s", pkg, own, tail(out.String()))
	}
	return own, nil
}

// pathAdapter is the user's own claude-agent-acp, when the PATH has a real
// one. One the PATH reaches only through a shim is passed over for AgentBox's
// own: mise's answer for it depends on configuration under the user's HOME and
// the directory it is asked from, which the lead's chat doesn't share.
func pathAdapter() string {
	path, err := exec.LookPath(ChatAdapters["claude"].Command)
	if err != nil || isShim(path) {
		return ""
	}
	return path
}

// adapterVersion is the version of the adapter installed in AgentBox's own
// directory, or "" when there is none.
func (m *Manager) adapterVersion() string {
	pkg := strings.TrimPrefix(ChatAdapters["claude"].Package, "npm:")
	name := pkg[:strings.LastIndex(pkg, "@")]
	raw, err := os.ReadFile(filepath.Join(m.toolsHome(), "node_modules", name, "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return ""
	}
	return manifest.Version
}

// pinnedVersion is the version in a mise package name like
// npm:@scope/name@1.2.3.
func pinnedVersion(pkg string) string {
	return pkg[strings.LastIndex(pkg, "@")+1:]
}

// systemPath is where the ordinary commands an installer calls live. The daemon
// may have been started by a desktop launcher with almost nothing on its PATH,
// and an installer that can't find `tr` fails in ways that say nothing useful.
const systemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// installEnv is the environment an installer runs in: the daemon's, with HOME
// pointed at AgentBox's own tools directory so nothing lands on the user's
// PATH, and with the system directories on PATH whatever the daemon inherited.
func (m *Manager) installEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = systemPath
	} else {
		path += string(os.PathListSeparator) + systemPath
	}
	env := append(os.Environ(), "HOME="+m.toolsHome(), "PATH="+path)
	return env
}

// installError says plainly when the machine is offline, rather than showing
// curl's or npm's own wording.
func installError(what string, err error, out string, ctx context.Context) error {
	if ctx.Err() != nil {
		return fmt.Errorf("installing %s took longer than %s: check this machine's connection", what, hostInstallTimeout)
	}
	if offline(out) {
		return fmt.Errorf("installing %s failed: this machine seems to be offline. The project chat needs it once, then works without a connection to install", what)
	}
	return fmt.Errorf("installing %s: %w: %s", what, err, tail(out))
}

func offline(out string) bool {
	out = strings.ToLower(out)
	for _, sign := range []string{
		"could not resolve host", "name or service not known", "temporary failure in name resolution",
		"network is unreachable", "connection refused", "connection timed out", "enotfound", "eai_again",
	} {
		if strings.Contains(out, sign) {
			return true
		}
	}
	return false
}

func usable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func tail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.Join(lines[max(len(lines)-3, 0):], " ")
}

// toolEnv keeps a version manager working when the lead's HOME isn't the user's.
//
// A tool installed with mise is usually reached through a shim, and a shim is
// mise itself, working out what to run from its own directories — which it
// looks for under HOME. The lead needs a private HOME, so those directories
// have to be named outright, or the shim fails with "not a valid shim" and the
// adapter dies before it says anything. The adapter is a Node script, and node
// is a shim too, so it isn't enough to resolve one binary.
//
// This doesn't widen what the lead can reach: these say where tools are
// installed, not what it may read. Its own HOME, and the permission rules in
// it, are untouched.
func toolEnv() []string {
	var env []string
	set := map[string]bool{}
	// Anything the user already chose wins.
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "MISE_") {
			env = append(env, kv)
			set[name] = true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return env
	}
	for _, dir := range []struct{ name, xdg, fallback string }{
		{"MISE_DATA_DIR", "XDG_DATA_HOME", ".local/share"},
		{"MISE_CONFIG_DIR", "XDG_CONFIG_HOME", ".config"},
		{"MISE_CACHE_DIR", "XDG_CACHE_HOME", ".cache"},
		{"MISE_STATE_DIR", "XDG_STATE_HOME", ".local/state"},
	} {
		if set[dir.name] {
			continue
		}
		base := os.Getenv(dir.xdg)
		if !filepath.IsAbs(base) {
			base = filepath.Join(home, dir.fallback)
		}
		env = append(env, dir.name+"="+filepath.Join(base, "mise"))
	}
	return env
}
