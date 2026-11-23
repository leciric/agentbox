package homehost

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"agentbox/internal/shim"
)

// Host is the helper's side: it runs what the daemon asks, as the user, on
// their computer.
type Host struct {
	Version string
	// Dir is the chat's working directory, holding the brief and Claude
	// Code's project settings and nothing of the user's: a CLAUDE.md in their
	// home folder would brief every Claude Code session they start under it.
	Dir string
	// Tools is where adapters the host doesn't have are installed with npm.
	Tools string
	// Self and Socket are this binary and the daemon's socket, which the MCP
	// server the session starts with runs and dials.
	Self, Socket string
	// Find finds the AI tools; nil is FindTools. Tests replace it.
	Find func() map[string]Tool

	mu    sync.Mutex
	procs map[string]*hostProc
	next  int
}

type hostProc struct {
	cmd    *exec.Cmd
	done   chan struct{}
	exit   int
	stderr *tail
}

// Serve answers the daemon on conn until it ends, then kills whatever it
// started: an adapter nobody can talk to has nothing left to do.
func (h *Host) Serve(ctx context.Context, conn net.Conn) error {
	sess, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
		case <-sess.CloseChan():
		}
		_ = sess.Close()
	}()
	for {
		s, err := sess.Accept()
		if err != nil {
			return nil
		}
		go h.handle(ctx, s)
	}
}

func (h *Host) handle(ctx context.Context, s net.Conn) {
	br := bufio.NewReader(s)
	var req request
	if err := readLine(br, &req); err != nil {
		_ = s.Close()
		return
	}
	switch req.Op {
	case opProbe:
		info := h.Info()
		_ = writeLine(s, reply{Info: &info})
	case opSpawn:
		if req.Spec == nil {
			_ = writeLine(s, reply{Error: "nothing to spawn"})
			break
		}
		h.spawn(ctx, s, br, *req.Spec)
		return // the stream is the process's now
	case opWait:
		p := h.proc(req.ID)
		if p == nil {
			_ = writeLine(s, reply{Error: "no such process"})
			break
		}
		<-p.done
		h.mu.Lock()
		delete(h.procs, req.ID)
		h.mu.Unlock()
		_ = writeLine(s, reply{Exit: &p.exit, Stderr: p.stderr.String()})
	case opKill:
		if p := h.proc(req.ID); p != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = writeLine(s, reply{})
	default:
		_ = writeLine(s, reply{Error: fmt.Sprintf("unknown request %q", req.Op)})
	}
	_ = s.Close()
}

func (h *Host) proc(id string) *hostProc {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.procs[id]
}

// Info is what the host has.
func (h *Host) Info() Info {
	name, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	find := h.Find
	if find == nil {
		find = FindTools
	}
	return Info{Version: h.Version, OS: runtime.GOOS, Host: name, Home: home, Dir: h.Dir, Tools: find()}
}

// spawn starts spec and carries its stdin and stdout over s.
func (h *Host) spawn(ctx context.Context, s net.Conn, br *bufio.Reader, spec Spec) {
	status := func(text string) { _ = writeLine(s, reply{Status: text}) }
	fail := func(err error) {
		_ = writeLine(s, reply{Error: err.Error()})
		_ = s.Close()
	}
	cmd, err := h.command(ctx, spec, status)
	if err != nil {
		fail(err)
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fail(err)
		return
	}
	// An os.Pipe, as chat.StartCommand has it: the last messages are read
	// even after the process exits.
	stdout, w, err := os.Pipe()
	if err != nil {
		fail(err)
		return
	}
	cmd.Stdout = w
	p := &hostProc{cmd: cmd, done: make(chan struct{}), stderr: &tail{max: 16 << 10}}
	cmd.Stderr = p.stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = w.Close()
		fail(err)
		return
	}
	_ = w.Close()
	h.mu.Lock()
	if h.procs == nil {
		h.procs = map[string]*hostProc{}
	}
	h.next++
	id := strconv.Itoa(h.next)
	h.procs[id] = p
	h.mu.Unlock()
	go func() {
		err := cmd.Wait()
		p.exit = cmd.ProcessState.ExitCode()
		if p.exit == 0 && err != nil {
			p.exit = -1
		}
		close(p.done)
	}()
	if err := writeLine(s, reply{ID: id, Cwd: cmd.Dir, MCP: h.mcp(spec.Session)}); err != nil {
		_ = cmd.Process.Kill()
		return
	}
	// The daemon closing its side is the adapter's stdin ending: it exits.
	go func() {
		_, _ = io.Copy(stdin, br)
		_ = stdin.Close()
	}()
	go func() {
		_, _ = io.Copy(s, stdout)
		_ = stdout.Close()
		_ = s.Close()
	}()
}

// mcp is the session's MCP server: AgentBox's own tools, by way of this
// binary, which carries its stdio to `agentbox mcp` in the VM.
func (h *Host) mcp(session string) []MCPServer {
	return []MCPServer{{
		Name: "agentbox", Command: h.Self, Args: []string{"home-host", "mcp"},
		Env: []string{SocketEnv + "=" + h.Socket, SessionEnv + "=" + session},
	}}
}

// sessionDir is where a Home chat session runs: the folder itself for the
// first, which the chat had before sessions, and one of its own for another.
func (h *Host) sessionDir(session string) (string, error) {
	if session == "" || session == "main" {
		return h.Dir, nil
	}
	if strings.ContainsAny(session, `/\.:`) {
		return "", fmt.Errorf("%q isn't a session", session)
	}
	return filepath.Join(h.Dir, "sessions", session), nil
}

// copilotArgs are what GitHub Copilot CLI is started with on the user's
// computer: ACP, on the version they have (a newer one it downloaded would be
// run in its place), and without its own telemetry.
var copilotArgs = []string{"--acp", "--no-auto-update", "--disable-client-telemetry"}

// geminiEnv is what Gemini CLI is started with on the user's computer.
var geminiEnv = []string{"GEMINI_CLI_NO_RELAUNCH=1", "GEMINI_TELEMETRY_ENABLED=false"}

// command is the adapter for spec, its working directory filled in.
func (h *Host) command(ctx context.Context, spec Spec, status func(string)) (*exec.Cmd, error) {
	find := h.Find
	if find == nil {
		find = FindTools
	}
	tool, ok := find()[spec.Tool]
	name := toolNames[spec.Tool]
	if !ok || tool.Path == "" {
		return nil, fmt.Errorf("%s isn't installed on this computer", or(name, spec.Tool))
	}
	dir, err := h.sessionDir(spec.Session)
	if err != nil {
		return nil, err
	}
	if err := writeFiles(dir, spec.Files); err != nil {
		return nil, err
	}
	env := append(os.Environ(), spec.Env...)
	if !tool.Login {
		env = append(env, spec.LoginEnv...)
	}
	var command string
	var args []string
	switch spec.Tool {
	case "claude":
		adapter, adapterEnv, err := h.adapter(ctx, env, spec.Package, "claude-agent-acp", name, status)
		if err != nil {
			return nil, err
		}
		// The adapter carries a Claude Code of its own; this runs the one
		// the user installed, with their settings and login.
		command, env = adapter, append(adapterEnv, "CLAUDE_CODE_EXECUTABLE="+tool.Path)
	case "codex":
		adapter, adapterEnv, err := h.adapter(ctx, env, spec.Package, "codex-acp", name, status)
		if err != nil {
			return nil, err
		}
		command, env = adapter, adapterEnv
	case "opencode":
		command, args = tool.Path, []string{"acp"}
	case "gemini":
		// In this process, not a child it would relaunch itself as, which
		// stopping the chat would leave running (agent.geminiNoRelaunch);
		// and without its OpenTelemetry, which the settings.json the
		// daemon sent also turns off, but which an untrusted folder's
		// settings can't.
		command, args = tool.Path, []string{"--acp"}
		env = append(env, geminiEnv...)
	case "copilot":
		// Its binary, not npm's loader around it (copilotNative).
		command, args = copilotNative(tool.Path), copilotArgs
	default:
		return nil, fmt.Errorf("the Home chat can't run %q on your computer", spec.Tool)
	}
	status("Starting " + name + " on your computer")
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd, nil
}

// writeFiles writes what the daemon sent into the working directory, inside
// it only.
func writeFiles(dir string, files map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for rel, content := range files {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			return fmt.Errorf("%q isn't inside the chat's folder", rel)
		}
		path := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// adapter finds an ACP adapter on the host, or installs pkg (an npm package
// at the version AgentBox pins) into Tools with the host's npm. It returns env
// with what runs the adapter in front of the PATH.
//
// Every program here must run without a version manager: the adapter runs in
// the chat's folder, where mise has no configuration to say what a shim means,
// and fails with "mise ERROR" (shim.Look). The adapter is a Node script whose
// `#!/usr/bin/env node` takes the first node on the PATH, which on a machine
// with mise is a shim too, so the real node's directory goes first on the PATH
// of the adapter and of the npm that installs it.
func (h *Host) adapter(ctx context.Context, env []string, pkg, command, tool string, status func(string)) (string, []string, error) {
	node := shim.Look("node")
	if node == "" {
		return "", nil, noNodeError(tool)
	}
	env = pathFirst(env, filepath.Dir(node))
	if path := shim.Look(command); path != "" {
		return path, env, nil
	}
	pkg = strings.TrimPrefix(pkg, "npm:")
	if pkg == "" {
		return "", nil, fmt.Errorf("%s's adapter, %s, isn't installed on this computer", tool, command)
	}
	own := filepath.Join(h.Tools, "node_modules", ".bin", command)
	if runtime.GOOS == "windows" {
		own += ".cmd"
	}
	at := strings.LastIndex(pkg, "@")
	if at > 0 && installedVersion(h.Tools, pkg[:at]) == pkg[at+1:] {
		if _, err := os.Stat(own); err == nil {
			return own, env, nil
		}
	}
	npm := shim.Look("npm")
	if npm == "" {
		return "", nil, fmt.Errorf("%s's adapter is an npm package, and Node.js isn't installed on this computer", tool)
	}
	status("Installing " + tool + "'s adapter on your computer, once")
	if err := os.MkdirAll(h.Tools, 0o700); err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	install := exec.CommandContext(ctx, npm, "install", "--silent", "--no-fund", "--no-audit", "--prefix", h.Tools, pkg)
	install.Env = env
	out, err := install.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("installing %s on your computer: %w: %s", pkg, err, lastLines(string(out)))
	}
	if _, err := os.Stat(own); err != nil {
		return "", nil, fmt.Errorf("installing %s on your computer: npm succeeded but %s isn't there", pkg, own)
	}
	return own, env, nil
}

// noNodeError says why the adapter has no node: there is none, or the PATH
// has one only as a shim mise can't resolve from the user's home, which the fix
// for is theirs to make.
func noNodeError(tool string) error {
	if path, err := exec.LookPath("node"); err == nil && shim.Is(path) {
		return fmt.Errorf("%s's adapter runs on Node.js, and the node on this computer's PATH is a shim (%s) that has no version set for your home folder: run `mise use -g node`, or install Node.js", tool, path)
	}
	return fmt.Errorf("%s's adapter runs on Node.js, which isn't installed on this computer", tool)
}

// pathFirst is env with dir at the front of its PATH. Of two entries for a
// variable the later wins, as it does for a process.
func pathFirst(env []string, dir string) []string {
	key, path := "PATH", ""
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && pathVar(name) {
			key, path = name, value
		}
	}
	if path != "" {
		dir += string(os.PathListSeparator) + path
	}
	return append(slices.Clone(env), key+"="+dir)
}

// pathVar reports whether name is the PATH variable: Windows' is Path.
func pathVar(name string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, "PATH")
	}
	return name == "PATH"
}

// installedVersion is the version of npm package name installed under dir.
func installedVersion(dir, name string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "node_modules", filepath.FromSlash(name), "package.json"))
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

var toolNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode", "gemini": "Gemini CLI", "copilot": "GitHub Copilot CLI"}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func lastLines(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.Join(lines[max(len(lines)-3, 0):], " ")
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
