// Package agentd is the small daemon an agent's pod runs. It prepares the
// worktree the pod's volume holds and keeps one session open to the master,
// over which the master runs commands, reads and writes files and reaches the
// ports the pod listens on.
//
// It has no policy of its own: the master decides what runs. The one line it
// keeps is the workspace's, because that is the difference between the tree the
// agent owns and the image it runs in.
package agentd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
)

// ErrNotSupported is what a build without Unix pseudo-terminals answers a
// terminal with. agentd runs in Linux pods; the Mac and Windows builds exist so
// this module's command line still installs there, not so they can run one.
var ErrNotSupported = errors.New("a terminal is not served on this platform")

// waitDelay is how long a finished command's output pipe is kept open for a
// child it left behind. It bounds how long a stream waits after the command it
// ran has ended.
const waitDelay = time.Second

// Config is what the pod tells agentd about itself, as the environment the
// master made the pod with.
type Config struct {
	// Ref is the agent, as <project>/<agent>.
	Ref string
	// MasterURL is where the master is reached from the cluster, with or
	// without the gateway's path on the end.
	MasterURL string
	// Branch is the branch the worktree is on.
	Branch string
	// BaseCommit is the commit the branch starts from on a first start.
	BaseCommit string
	// Workspace is the directory the worktree is in.
	Workspace string
	// TokenFile holds the bearer token the session is opened with. It is read
	// once, and never written to a log or an error.
	TokenFile string
}

// FromEnv reads the config from the environment the master made the pod with.
// It names every variable it needs and does not find, so a pod that starts
// wrong says which part of its spec is missing.
func FromEnv() (Config, error) {
	cfg := Config{
		Ref:        strings.TrimSpace(os.Getenv(k8s.EnvRef)),
		MasterURL:  strings.TrimSpace(os.Getenv(k8s.EnvMasterURL)),
		Branch:     strings.TrimSpace(os.Getenv(k8s.EnvBranch)),
		BaseCommit: strings.TrimSpace(os.Getenv(k8s.EnvBaseCommit)),
		Workspace:  strings.TrimSpace(os.Getenv(k8s.EnvWorkspace)),
		TokenFile:  strings.TrimSpace(os.Getenv(k8s.EnvTokenFile)),
	}
	if cfg.Workspace == "" {
		cfg.Workspace = k8s.WorkspaceDir
	}
	if cfg.TokenFile == "" {
		// k8s.TokenFile is where the pod's token Secret is mounted. It is not
		// /run/agentbox/token, because /run/agentbox is agentd's own writable
		// emptyDir and a read-only Secret cannot share it.
		cfg.TokenFile = k8s.TokenFile
	}
	if err := cfg.check(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// check names the first thing a pod cannot start without.
func (c Config) check() error {
	switch {
	case c.Ref == "":
		return fmt.Errorf("%s is not set: it is the agent, as <project>/<agent>", k8s.EnvRef)
	case c.MasterURL == "":
		return fmt.Errorf("%s is not set: it is the master's base URL, like https://monster.example.ts.net", k8s.EnvMasterURL)
	case c.Branch == "":
		return fmt.Errorf("%s is not set: it is the branch the agent works on", k8s.EnvBranch)
	case c.BaseCommit == "":
		return fmt.Errorf("%s is not set: it is the commit the branch starts from", k8s.EnvBaseCommit)
	}
	if project, agent, ok := strings.Cut(c.Ref, "/"); !ok || project == "" || agent == "" {
		return fmt.Errorf("%s is %q, and it is <project>/<agent>", k8s.EnvRef, c.Ref)
	}
	if !strings.HasPrefix(c.MasterURL, "http://") && !strings.HasPrefix(c.MasterURL, "https://") {
		return fmt.Errorf("%s is %q, and it is an http or https base URL", k8s.EnvMasterURL, c.MasterURL)
	}
	return nil
}

// Run prepares the workspace and then serves the master's streams until ctx is
// cancelled. A master that restarts or a network that blinks costs it a
// reconnect, not an error: gateway.Client waits and tries again.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.check(); err != nil {
		return err
	}
	token, err := readToken(cfg.TokenFile)
	if err != nil {
		return err
	}
	logf("agentd: %s on %s, master %s", cfg.Ref, cfg.Branch, cfg.MasterURL)
	ws := Workspace{Dir: cfg.Workspace, Branch: cfg.Branch, Base: cfg.BaseCommit, Log: logf}
	if err := ws.Prepare(ctx); err != nil {
		return err
	}
	h := &handler{workspace: ws}
	client := &gateway.Client{
		URL:    sessionURL(cfg.MasterURL),
		Token:  token,
		Handle: h.handle,
		Log:    logf,
	}
	return client.Run(ctx)
}

// sessionURL is the master's base URL, without the gateway's path when it was
// given with one and without a trailing slash: gateway.Client appends the path
// itself, once.
func sessionURL(master string) string {
	base := strings.TrimRight(strings.TrimSpace(master), "/")
	base = strings.TrimSuffix(base, gateway.Path)
	return strings.TrimRight(base, "/")
}

// readToken reads the session token. Only the file's name can reach an error:
// the value is the one thing in this process that is nobody's to print.
func readToken(file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("%s is not set: it is where the pod's token is", k8s.EnvTokenFile)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading the session token from %s: %w", file, err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("%s holds no token", file)
	}
	return token, nil
}

// logf writes to the pod's log, which is what `kubectl logs` shows.
func logf(format string, args ...any) {
	log.Printf(format, args...)
}

// handler serves the operations the master asks for. It holds only the
// workspace, so streams run side by side.
type handler struct {
	workspace Workspace
}

// handle serves one stream: the operation its header names, then its frames.
func (h *handler) handle(ctx context.Context, head gateway.Header, s *gateway.Stream) error {
	switch head.Op {
	case gateway.OpExec:
		return h.exec(ctx, head, s)
	case gateway.OpRead:
		return h.read(head, s)
	case gateway.OpWrite:
		return h.write(head, s)
	case gateway.OpList:
		return h.list(head, s)
	case gateway.OpDial:
		return h.dial(ctx, head, s)
	case gateway.OpAttach:
		return h.attach(ctx, head, s)
	case gateway.OpControl:
		return refuse(s, 2, "control is not served by agentd yet")
	}
	return refuse(s, 2, "agentd doesn't know the op %q", head.Op)
}

// exec runs the header's command in the pod. Its stdin is the stream's stdin
// frames and its output the stdout and stderr ones; the exit code ends it.
func (h *handler) exec(ctx context.Context, head gateway.Header, s *gateway.Stream) error {
	if len(head.Argv) == 0 {
		return refuse(s, 2, "exec needs an argv")
	}
	pod := currentPodUser()
	if !pod.canBecome(head.User) {
		return refuse(s, 2, "%v", pod.refuseUser(head.User))
	}
	// The command's context is the stream's: a stream the master closes takes
	// the command with it, which is the whole point of one process group.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, head.Argv[0], head.Argv[1:]...)
	cmd.Dir = h.workspace.Dir
	if head.Dir != "" {
		cmd.Dir = head.Dir
	}
	cmd.Env = append(os.Environ(), head.Env...)
	ownGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	// A child the command leaves behind can hold its output pipe, and waiting
	// for it holds the stream. A second is enough for one last line, and a
	// command that succeeded keeps its own exit code either way (waitCode).
	cmd.WaitDelay = waitDelay
	cmd.Stdout = frameWriter{s: s, kind: gateway.FrameStdout}
	cmd.Stderr = frameWriter{s: s, kind: gateway.FrameStderr}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return refuse(s, 1, "opening the command's stdin: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return refuse(s, 127, "running %s: %v", head.Argv[0], err)
	}
	// The master's stdin frames are the command's input. The read ends when the
	// master half-closes, which closes stdin so the command sees EOF, and fails
	// when the stream is gone, which cancels the command.
	go func() {
		_, err := io.Copy(stdin, gateway.NewFrameReader(s, gateway.FrameStdin))
		_ = stdin.Close()
		if err != nil {
			stop()
		}
	}()
	err = cmd.Wait()
	return s.Exit(waitCode(cmd, err))
}

// terminal is a program on a pseudo-terminal in the pod, from the stream's
// side: what it prints is read, what is typed is written, and a resize changes
// the window it draws in. The Unix build implements it; the others have none.
type terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	// Wait ends when the program does, and tells how it ended.
	Wait() error
	// Kill ends the program and everything it started.
	Kill() error
}

// attach runs the header's program on a terminal, which is where the app's
// terminal pane and its tmux client live. Unlike exec's, an attach stream does
// not end with its input: the app keeps typing while the program keeps
// printing, and only one of them ending ends the stream.
func (h *handler) attach(ctx context.Context, head gateway.Header, s *gateway.Stream) error {
	argv := head.Argv
	if len(argv) == 0 {
		// The app normally names the tmux command itself, and a bare shell is
		// what a terminal opened without one is for.
		argv = []string{loginShell()}
	}
	cols, rows := head.Cols, head.Rows
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	dir := h.workspace.Dir
	if head.Dir != "" {
		dir = head.Dir
	}
	term, err := startTerminal(argv, dir, terminalEnv(head.Env), cols, rows)
	if err != nil {
		if errors.Is(err, ErrNotSupported) {
			return refuse(s, 2, "%v", err)
		}
		return refuse(s, 127, "running %s: %v", argv[0], err)
	}
	// Closing the master side of the terminal hangs up whatever is still on
	// it, which is the last resort when a program outlives its stream.
	defer func() { _ = term.Close() }()

	// What the program prints goes out as stdout frames. One goroutine keeps
	// the bytes in order, and the read's buffer stays under gateway.MaxFrame so
	// one read is one frame. A pty read ends with EIO or EOF once the program
	// has exited and its side is gone, which is the end of the output, not a
	// fault to report.
	output := make(chan struct{})
	go func() {
		defer close(output)
		buf := make([]byte, 32<<10)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				if werr := s.Write(gateway.FrameStdout, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Wait runs exactly once, in its own goroutine, so the exit code is ready
	// the moment the program ends and a kill from either ending below reaps it.
	exited := make(chan int, 1)
	go func() { exited <- exitCode(term.Wait()) }()

	// The stream's other direction: stdin frames are the keyboard, and resize
	// frames are the app's window changing. The loop ends when the stream does.
	// An attach stream is closed whole, never half (a yamux stream has no
	// half-close), so the end of it means the app is gone and the terminal is
	// killed below rather than left as a tmux client nobody can see.
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for {
			kind, payload, err := s.Read()
			if err != nil {
				return
			}
			switch kind {
			case gateway.FrameStdin:
				if _, err := term.Write(payload); err != nil {
					return
				}
			case gateway.FrameResize:
				if len(payload) != 4 {
					continue
				}
				cols := binary.BigEndian.Uint16(payload[0:2])
				rows := binary.BigEndian.Uint16(payload[2:4])
				if cols == 0 || rows == 0 {
					continue
				}
				_ = term.Resize(cols, rows)
			}
		}
	}()

	var code int
	select {
	case code = <-exited:
	case <-ended:
		_ = term.Kill()
		code = <-exited
	case <-ctx.Done():
		_ = term.Kill()
		code = <-exited
	}
	// The output goroutine writes the program's last bytes as frames of its
	// own, so the exit frame goes only once it has had the moment it needs, on
	// every path: the program's ending and the stream's both leave bytes in the
	// terminal.
	drainOutput(output)
	return s.Exit(code)
}

// drainOutput waits for the output goroutine, at most the same second a
// finished command gets: a child the program left behind can hold the terminal
// open, and the exit frame must still go.
func drainOutput(output <-chan struct{}) {
	select {
	case <-output:
	case <-time.After(time.Second):
	}
}

// terminalEnv is what the terminal's program runs in: the pod's environment,
// then the header's on top, which is how the app adds to it. A TERM is added
// when neither gives one a value, because tmux and every full-screen tool
// refuse to draw without it and a pod's environment need not have one.
func terminalEnv(extra []string) []string {
	env := append(os.Environ(), extra...)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "TERM="); ok && v != "" {
			return env
		}
	}
	return append(env, "TERM=xterm-256color")
}

// loginShell is the shell a terminal opens when the header names no program:
// the pod's SHELL when it was given one, else the shell the pod's user is
// recorded with, and /bin/sh when neither says. A pod is given no SHELL, so
// the passwd entry is usually the one that answers.
func loginShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	if shell := passwdShell(); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// read streams a file's bytes. A relative path is in the workspace, as a
// write's is.
func (h *handler) read(head gateway.Header, s *gateway.Stream) error {
	if head.Path == "" {
		return refuse(s, 2, "read needs a path")
	}
	path := head.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.workspace.Dir, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return refuse(s, 1, "%v", err)
	}
	if info.IsDir() {
		return refuse(s, 1, "%s is a directory, and read is for files", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return refuse(s, 1, "%v", err)
	}
	defer func() { _ = f.Close() }()
	if err := gateway.CopyFrames(s, gateway.FrameData, f); err != nil {
		return err
	}
	return s.Exit(0)
}

// write makes the stream's data frames a file in the workspace, its home or
// agentd's run directory. A file the master names a mode for gets it, which is
// how a script it writes is made executable; a new file is otherwise 0600,
// because the pod's tree is the agent's. A file the master says belongs to
// another user is refused: the pod's agent writes it, and cannot claim it is
// someone else's.
func (h *handler) write(head gateway.Header, s *gateway.Stream) error {
	pod := currentPodUser()
	if err := pod.checkOwner(head.UID, head.GID); err != nil {
		return refuse(s, 2, "%v", err)
	}
	target, err := h.workspace.writeTarget(head.Path)
	if err != nil {
		return refuse(s, 1, "%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return refuse(s, 1, "creating the directory %s the write is for: %v", filepath.Dir(target), err)
	}
	perm := os.FileMode(0o600)
	if head.Mode != 0 {
		perm = os.FileMode(head.Mode).Perm()
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return refuse(s, 1, "%v", err)
	}
	_, copyErr := io.Copy(f, gateway.NewFrameReader(s, gateway.FrameData))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return refuse(s, 1, "writing %s: %v", target, copyErr)
	case closeErr != nil:
		return refuse(s, 1, "writing %s: %v", target, closeErr)
	}
	return s.Exit(0)
}

// list answers a directory as JSON: each entry's name, size, mode and whether
// it is a directory. The workspace is listed when the header names no path, and
// a relative one is in the workspace.
func (h *handler) list(head gateway.Header, s *gateway.Stream) error {
	dir := head.Path
	if dir == "" {
		dir = h.workspace.Dir
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(h.workspace.Dir, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return s.WriteResponse(gateway.Response{Error: err.Error()})
	}
	answer := gateway.Response{Entries: make([]gateway.Entry, 0, len(entries))}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return s.WriteResponse(gateway.Response{Error: err.Error()})
		}
		answer.Entries = append(answer.Entries, gateway.Entry{
			Name: entry.Name(),
			Size: info.Size(),
			Mode: uint32(info.Mode()),
			Dir:  entry.IsDir(),
		})
	}
	return s.WriteResponse(answer)
}

// dial carries a connection both ways: what the master sends is written to the
// far end, and what the far end answers goes back in frames. This is how the
// master reaches the desktop's VNC port or the browser's debugging port, which
// listen only in the pod.
func (h *handler) dial(ctx context.Context, head gateway.Header, s *gateway.Stream) error {
	if head.Addr == "" {
		return refuse(s, 2, "dial needs an address")
	}
	network := head.Network
	if network == "" {
		network = "tcp"
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, head.Addr)
	if err != nil {
		return refuse(s, 1, "connecting to %s: %v", head.Addr, err)
	}
	defer func() { _ = conn.Close() }()
	// When the master half-closes, the far end is told with a CloseWrite, so a
	// server that reads to EOF sees the end and can answer.
	go func() {
		for {
			kind, payload, err := s.Read()
			if err != nil {
				if half, ok := conn.(interface{ CloseWrite() error }); ok {
					_ = half.CloseWrite()
				} else {
					_ = conn.Close()
				}
				return
			}
			if kind != gateway.FrameData {
				continue
			}
			if _, err := conn.Write(payload); err != nil {
				_ = conn.Close()
				return
			}
		}
	}()
	buf := make([]byte, 32<<10)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if werr := s.Write(gateway.FrameData, buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			// The far end ended: the master is told with a half-close, so what
			// it already sent still reaches the connection before the stream
			// goes.
			return s.CloseWrite()
		}
	}
}

// refuse ends a stream with an exit code and one line on stderr.
func refuse(s *gateway.Stream, code int, format string, args ...any) error {
	if err := s.Write(gateway.FrameStderr, []byte(fmt.Sprintf(format, args...)+"\n")); err != nil {
		return err
	}
	return s.Exit(code)
}

// frameWriter is a command's output as frames. A command writes its stdout and
// stderr from two goroutines, so the writes meet the stream's own lock.
type frameWriter struct {
	s    *gateway.Stream
	kind byte
}

func (w frameWriter) Write(p []byte) (int, error) {
	if err := w.s.Write(w.kind, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// exitCode is what a finished command exits with. A command killed by a signal
// exits with 128 and the signal, as a shell reports it. A platform whose wait
// status does not say which signal (Windows) never reports one: the process's
// own code is used, and 1 when even that is unknown.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 1
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	if code := exit.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

// waitCode is what a command that has finished exits with. WaitDelay closes an
// output pipe a child the command left behind kept open, and Wait then reports
// ErrWaitDelay for a command that itself succeeded: its own exit code is the
// answer, not the failure the wait reports.
func waitCode(cmd *exec.Cmd, err error) int {
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			return code
		}
	}
	return exitCode(err)
}

// podUser is the user agentd runs as. A pod's container runs as one user, the
// agent's, and nothing in it can become another: a stream that names a user is
// refused rather than quietly run as this one.
type podUser struct {
	// name is what this pod calls its user: USER when the image sets it, and
	// the passwd entry's name when it doesn't.
	name string
	// login is the name the uid has in the passwd file, whatever USER says.
	login string
	// uid and gid are the pod's own, and known is false when they can't be
	// read, which a write must not guess at.
	uid, gid int
	known    bool
}

// currentPodUser is who this process runs as.
func currentPodUser() podUser {
	u := podUser{name: strings.TrimSpace(os.Getenv("USER"))}
	if me, err := user.Current(); err == nil {
		u.login = me.Username
		u.uid, _ = strconv.Atoi(me.Uid)
		u.gid, _ = strconv.Atoi(me.Gid)
		u.known = true
	}
	if u.name == "" {
		u.name = u.login
	}
	return u
}

// canBecome reports whether an exec's user is the pod's own. Empty means "the
// user the pod runs as", which is the only one there is.
func (u podUser) canBecome(want string) bool {
	if want == "" {
		return true
	}
	return (u.name != "" && want == u.name) || (u.login != "" && want == u.login)
}

// refuseUser is the line a stream that names another user gets.
func (u podUser) refuseUser(want string) error {
	return fmt.Errorf("this pod runs as %s and cannot become %s", u.describe(), want)
}

// checkOwner refuses a file that would be owned by a user this pod is not. A
// header cannot tell a zero uid from one it never set, because zero is left out
// of the JSON, so zero means "the pod's own": the caller that names no owner
// gets the agent's, and one that names another is refused rather than written
// as the agent and reported as something else.
func (u podUser) checkOwner(uid, gid int) error {
	if uid == 0 && gid == 0 {
		return nil
	}
	if u.known && (uid == 0 || uid == u.uid) && (gid == 0 || gid == u.gid) {
		return nil
	}
	if !u.known {
		return fmt.Errorf("this pod runs as %s, and a write cannot be owned by %d:%d", u.describe(), uid, gid)
	}
	return fmt.Errorf("this pod runs as %s (%d:%d) and cannot write a file owned by %d:%d", u.describe(), u.uid, u.gid, uid, gid)
}

// describe is what an error calls the user the pod runs as.
func (u podUser) describe() string {
	if u.name != "" {
		return u.name
	}
	return "the agent"
}
