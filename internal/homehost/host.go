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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
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
		adapter, err := h.adapter(ctx, spec.Package, "claude-agent-acp", name, status)
		if err != nil {
			return nil, err
		}
		// The adapter carries a Claude Code of its own; this runs the one
		// the user installed, with their settings and login.
		command, env = adapter, append(env, "CLAUDE_CODE_EXECUTABLE="+tool.Path)
	case "codex":
		adapter, err := h.adapter(ctx, spec.Package, "codex-acp", name, status)
		if err != nil {
			return nil, err
		}
		command = adapter
	case "opencode":
		command, args = tool.Path, []string{"acp"}
	case "gemini":
		command, args = tool.Path, []string{"--acp"}
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
// at the version AgentBox pins) into Tools with the host's npm.
func (h *Host) adapter(ctx context.Context, pkg, command, tool string, status func(string)) (string, error) {
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	pkg = strings.TrimPrefix(pkg, "npm:")
	if pkg == "" {
		return "", fmt.Errorf("%s's adapter, %s, isn't installed on this computer", tool, command)
	}
	own := filepath.Join(h.Tools, "node_modules", ".bin", command)
	if runtime.GOOS == "windows" {
		own += ".cmd"
	}
	at := strings.LastIndex(pkg, "@")
	if at > 0 && installedVersion(h.Tools, pkg[:at]) == pkg[at+1:] {
		if _, err := os.Stat(own); err == nil {
			return own, nil
		}
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("%s's adapter is an npm package, and Node.js isn't installed on this computer", tool)
	}
	status("Installing " + tool + "'s adapter on your computer, once")
	if err := os.MkdirAll(h.Tools, 0o700); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, npm, "install", "--silent", "--no-fund", "--no-audit", "--prefix", h.Tools, pkg).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("installing %s on your computer: %w: %s", pkg, err, lastLines(string(out)))
	}
	if _, err := os.Stat(own); err != nil {
		return "", fmt.Errorf("installing %s on your computer: npm succeeded but %s isn't there", pkg, own)
	}
	return own, nil
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

var toolNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode", "gemini": "Gemini CLI"}

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
