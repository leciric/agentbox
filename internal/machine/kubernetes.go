package machine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/incus"
)

// Pods is what a pod's machine can't do by itself: start, stop, destroy and
// recreate change rows and the cluster, which the daemon owns; Info reads what it
// last saw. The gateway carries only bytes.
type Pods interface {
	Start(ctx context.Context, ref gateway.Ref) error
	Stop(ctx context.Context, ref gateway.Ref) error
	Destroy(ctx context.Context, ref gateway.Ref) error
	// Recreate deletes the pod and asks for a new one on the same claim, at the
	// image the cluster runs now: the row's pinned image is dropped with it.
	Recreate(ctx context.Context, ref gateway.Ref) error
	// Info reads what the daemon last saw; State is "Running" or "Stopped".
	Info(ctx context.Context, ref gateway.Ref) (Info, error)

	// Snapshot archives the agent's claim to the master and Snapshots lists the
	// archives it holds. RestoreSnapshot prepares a new claim from one and
	// moves the agent onto it, and PrepareClaim primes an empty agent's claim
	// from another agent's snapshot, which is how a fork starts on a cluster.
	Snapshot(ctx context.Context, ref gateway.Ref, name string) error
	Snapshots(ctx context.Context, ref gateway.Ref) ([]Snapshot, error)
	RestoreSnapshot(ctx context.Context, ref gateway.Ref, name string) error
	DeleteSnapshot(ctx context.Context, ref gateway.Ref, name string) error
	HasSnapshot(ctx context.Context, ref gateway.Ref, name string) (bool, error)
	PrepareClaim(ctx context.Context, ref gateway.Ref, claim, from string) error
	SnapshotGit(ctx context.Context, ref gateway.Ref, name, refName, commit, head string) error
	SnapshotGitInfo(ctx context.Context, ref gateway.Ref, name string) (commit, head string, err error)
}

// Kubernetes is the runtime of one cluster: every agent there is a pod, and
// the pod's agentd answers over the gateway session the pod opened to this
// daemon.
type Kubernetes struct {
	sessions *gateway.Server
	pods     Pods
}

// NewKubernetes is the runtime over the daemon's gateway sessions and the
// pods' rows.
func NewKubernetes(sessions *gateway.Server, pods Pods) *Kubernetes {
	return &Kubernetes{sessions: sessions, pods: pods}
}

func (r *Kubernetes) Kind() Kind { return KindKubernetes }

// Machine is the machine for an agent ref, project/agent. A ref that doesn't
// read that way is kept as it is: Runtime.Machine must not fail, so the error
// waits until a method is called, where it can name the ref.
func (r *Kubernetes) Machine(ref string) Machine {
	m := podMachine{sessions: r.sessions, pods: r.pods, ref: ref}
	m.target, m.bad = parseRef(ref)
	return m
}

var _ Machine = podMachine{}
var _ Recreator = podMachine{}

// podMachine is one agent's pod, reached through its gateway session.
type podMachine struct {
	sessions *gateway.Server
	pods     Pods
	ref      string
	target   gateway.Ref
	// bad is why ref could not be read, and is the error every method
	// returns when it is set.
	bad error
}

func (m podMachine) Ref() string { return m.ref }

func (m podMachine) Kind() Kind { return KindKubernetes }

// parseRef reads the project/agent ref the manager keeps for a pod.
func parseRef(ref string) (gateway.Ref, error) {
	project, agent, ok := strings.Cut(ref, "/")
	if !ok || project == "" || agent == "" {
		return gateway.Ref{}, fmt.Errorf("the agent ref %q is not project/agent", ref)
	}
	return gateway.Ref{Project: project, Agent: agent}, nil
}

func (m podMachine) Info(ctx context.Context) (Info, error) {
	if err := m.bad; err != nil {
		return Info{}, err
	}
	return m.pods.Info(ctx, m.target)
}

func (m podMachine) WaitReady(ctx context.Context, timeout time.Duration) (Info, error) {
	if err := m.bad; err != nil {
		return Info{}, err
	}
	if _, err := m.pods.Info(ctx, m.target); err != nil {
		return Info{}, err
	}
	// A pod is ready when its agentd has connected: the pod can be Running
	// while its connection is still being made, and nothing reaches it until
	// the session exists.
	if err := m.sessions.WaitFor(ctx, m.target, timeout); err != nil {
		return Info{}, err
	}
	return m.pods.Info(ctx, m.target)
}

func (m podMachine) Start(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.Start(ctx, m.target)
}

func (m podMachine) Stop(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.Stop(ctx, m.target)
}

func (m podMachine) StopWithin(ctx context.Context, timeout time.Duration) error {
	if err := m.bad; err != nil {
		return err
	}
	// A pod has no graceful shutdown to wait for: stopping it is deleting it,
	// which the cluster does once.
	return m.pods.Stop(ctx, m.target)
}

func (m podMachine) ForceStop(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.Stop(ctx, m.target)
}

func (m podMachine) Pause(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return fmt.Errorf("a pod is stopped, not frozen, and stopping the agent is what pauses it: %w", ErrNotSupported)
}

func (m podMachine) Resume(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return fmt.Errorf("a pod is started, not resumed, and starting the agent is what brings it back: %w", ErrNotSupported)
}

func (m podMachine) Destroy(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.Destroy(ctx, m.target)
}

// Recreate replaces the pod, keeping its claim: the machine's files survive, the
// pod's processes and its container filesystem do not, and the new pod runs the
// image the cluster points at now.
func (m podMachine) Recreate(ctx context.Context) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.Recreate(ctx, m.target)
}

// Exec runs a command as root and returns what it printed on both streams,
// interleaved as they arrived.
func (m podMachine) Exec(ctx context.Context, command ...string) (string, error) {
	var out bytes.Buffer
	err := m.execStream(ctx, gateway.Header{Op: gateway.OpExec, Argv: command}, nil, &out, &out)
	return out.String(), err
}

func (m podMachine) ExecInput(ctx context.Context, stdin io.Reader, command ...string) (string, error) {
	var out bytes.Buffer
	err := m.execStream(ctx, gateway.Header{Op: gateway.OpExec, Argv: command}, stdin, &out, &out)
	return out.String(), err
}

// UserExec runs one command as user. Not a login shell: agentd already runs as
// the agent, and Debian's /etc/profile resets PATH in one, which drops the
// image's /usr/local/lib/nodejs/bin and with it node and every ACP adapter npm
// installed there. sh -c keeps the PATH the image sets, which is the one the
// terminal's tmux already gets.
func (m podMachine) UserExec(ctx context.Context, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	return m.execStream(ctx, gateway.Header{
		Op:   gateway.OpExec,
		Argv: []string{"sh", "-c", command},
		User: user,
	}, stdin, stdout, stderr)
}

// execStream opens one exec stream, sends stdin if there is any, and reads the
// frames until the pod says how the command ended.
func (m podMachine) execStream(ctx context.Context, h gateway.Header, stdin io.Reader, stdout, stderr io.Writer) error {
	s, err := m.stream(ctx, h)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	stop := watchContext(ctx, s)
	defer stop()

	if stdin == nil {
		// A stream has no frame that ends input, so the half-close is what
		// tells the pod its command's stdin is empty. Without it a command
		// that reads stdin waits for a frame that will never come.
		if err := s.CloseWrite(); err != nil {
			return err
		}
	} else {
		go func() {
			// The command may stop reading before the reader ends, so the
			// stream is closed when the exit frame arrives and this copy is
			// not waited for: its error is not the command's.
			if err := gateway.CopyFrames(s, gateway.FrameStdin, stdin); err == nil {
				_ = s.CloseWrite()
			}
		}()
	}
	for {
		kind, payload, err := s.Read()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		switch kind {
		case gateway.FrameStdout:
			if stdout != nil {
				if _, err := stdout.Write(payload); err != nil {
					return err
				}
			}
		case gateway.FrameStderr:
			if stderr != nil {
				if _, err := stderr.Write(payload); err != nil {
					return err
				}
			}
		case gateway.FrameExit:
			code := 0
			if len(payload) > 0 {
				code = int(payload[0])
			}
			if code != 0 {
				return exitStatus(code)
			}
			return nil
		}
	}
}

func (m podMachine) Attach(ctx context.Context, opts AttachOptions) (Terminal, error) {
	s, err := m.stream(ctx, gateway.Header{
		Op:   gateway.OpAttach,
		Argv: opts.Argv,
		Cols: opts.Cols,
		Rows: opts.Rows,
	})
	if err != nil {
		return nil, err
	}
	t := &podTerminal{stream: s, done: make(chan struct{})}
	// A terminal lives past this call, so its context is watched here rather
	// than in a deferred stop: the caller ending the session closes it.
	go func() {
		select {
		case <-ctx.Done():
			_ = t.stream.Close()
		case <-t.done:
		}
	}()
	return t, nil
}

// podTerminal is a terminal on the pod: frames from the pty are this side's
// bytes, and what is typed goes back as stdin.
type podTerminal struct {
	stream  *gateway.Stream
	pending []byte
	// code is the exit frame's status. A pty has one stream, so the pod writes
	// everything to stdout; the exit frame is the only thing that ends Read.
	// It is atomic because a caller may close the terminal while another
	// goroutine is still reading it.
	code atomic.Int32
	done chan struct{}
	once sync.Once
	err  error
}

func (t *podTerminal) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(t.pending) == 0 {
		kind, payload, err := t.stream.Read()
		if err != nil {
			return 0, err
		}
		switch kind {
		case gateway.FrameStdout:
			if len(payload) == 0 {
				continue
			}
			t.pending = payload
		case gateway.FrameExit:
			if len(payload) > 0 {
				t.code.Store(int32(payload[0]))
			}
			return 0, io.EOF
		}
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *podTerminal) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := t.stream.Write(gateway.FrameStdin, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize sends the terminal's size as two big-endian uint16s, columns first.
func (t *podTerminal) Resize(cols, rows uint16) error {
	var size [4]byte
	binary.BigEndian.PutUint16(size[0:2], cols)
	binary.BigEndian.PutUint16(size[2:4], rows)
	return t.stream.Write(gateway.FrameResize, size[:])
}

func (t *podTerminal) Close() error {
	t.once.Do(func() {
		close(t.done)
		t.err = t.stream.Close()
	})
	if t.err != nil {
		return t.err
	}
	// Read can only end with io.EOF at the exit frame, and there is no later
	// Read to report a failure from. Close is the caller's last call, so the
	// code is reported here, and only when closing the stream itself worked.
	if code := t.code.Load(); code != 0 {
		return exitStatus(int(code))
	}
	return nil
}

// WriteFile writes content to path through one write stream: the header names
// the file and its owner, the data frames carry it, and the exit frame says
// whether agentd wrote it.
func (m podMachine) WriteFile(ctx context.Context, path string, content []byte, uid, gid int, mode os.FileMode) error {
	s, err := m.stream(ctx, gateway.Header{
		Op:   gateway.OpWrite,
		Path: path,
		UID:  uid,
		GID:  gid,
		Mode: uint32(mode.Perm()),
	})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	stop := watchContext(ctx, s)
	defer stop()

	if err := gateway.CopyFrames(s, gateway.FrameData, bytes.NewReader(content)); err != nil {
		return err
	}
	if err := s.CloseWrite(); err != nil {
		return err
	}
	var said strings.Builder
	err = m.awaitExit(ctx, s, &said)
	if err != nil {
		if msg := strings.TrimSpace(said.String()); msg != "" {
			return fmt.Errorf("%s: %w", msg, err)
		}
	}
	return err
}

// awaitExit reads a stream that carries no answer but its exit code, keeping
// anything the pod wrote to stderr so a failure can say why.
func (m podMachine) awaitExit(ctx context.Context, s *gateway.Stream, stderr io.Writer) error {
	for {
		kind, payload, err := s.Read()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		switch kind {
		case gateway.FrameStderr:
			if stderr != nil {
				if _, err := stderr.Write(payload); err != nil {
					return err
				}
			}
		case gateway.FrameExit:
			code := 0
			if len(payload) > 0 {
				code = int(payload[0])
			}
			if code != 0 {
				return exitStatus(code)
			}
			return nil
		}
	}
}

// PullFile copies a file out of the pod into a temporary file beside target,
// and renames it into place only once the whole file has arrived: a failed
// pull leaves no half file where the caller expects one.
func (m podMachine) PullFile(ctx context.Context, file, target string) error {
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		// Same as Incus's PullFile: a directory target receives the file under
		// the name it has in the machine.
		target = filepath.Join(target, path.Base(file))
	}
	s, err := m.stream(ctx, gateway.Header{Op: gateway.OpRead, Path: file})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	stop := watchContext(ctx, s)
	defer stop()

	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	kept := false
	defer func() {
		if !kept {
			_ = os.Remove(name)
		}
	}()
	var said strings.Builder
	for {
		kind, payload, err := s.Read()
		if err != nil {
			_ = tmp.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		switch kind {
		case gateway.FrameData:
			if _, err := tmp.Write(payload); err != nil {
				_ = tmp.Close()
				return err
			}
		case gateway.FrameStderr:
			said.Write(payload)
		case gateway.FrameExit:
			code := 0
			if len(payload) > 0 {
				code = int(payload[0])
			}
			if err := tmp.Close(); err != nil {
				return err
			}
			if code != 0 {
				if msg := strings.TrimSpace(said.String()); msg != "" {
					return fmt.Errorf("%s: %w", msg, exitStatus(code))
				}
				return exitStatus(code)
			}
			if err := os.Rename(name, target); err != nil {
				return err
			}
			kept = true
			return nil
		}
	}
}

// PullDir copies a directory out of the pod: tar writes it on the pod's side,
// and archive/tar unpacks it under dir on this one.
func (m podMachine) PullDir(ctx context.Context, from, dir string) error {
	if err := m.bad; err != nil {
		return err
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := m.UserExec(ctx, "", "tar -C "+quoteForShell(from)+" -cf - .", nil, pw, nil)
		_ = pw.Close()
		done <- err
	}()
	err := extractTar(pr, dir)
	if err != nil {
		_ = pr.CloseWithError(err)
	} else {
		_ = pr.Close()
	}
	// The command's own failure, such as no tar or no such directory, is the
	// more useful error when unpacking saw an empty stream.
	commandErr := <-done
	if err != nil {
		return err
	}
	return commandErr
}

// extractTar unpacks r under dir, refusing anything that would land outside it.
// The pod's tar writes "." as the root, so every name is relative to dir.
func extractTar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(filepath.ToSlash(hdr.Name))
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("%s: the entry would land outside %s", hdr.Name, dir)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if rel, err := filepath.Rel(dir, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s: the entry would land outside %s", hdr.Name, dir)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, modeOr(hdr.FileInfo().Mode().Perm(), 0o755)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, modeOr(hdr.FileInfo().Mode().Perm(), 0o644))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// A symlink is skipped rather than recreated: its target can point
			// anywhere, and a later entry written through it would leave dir.
			return fmt.Errorf("%s: a symlink in the agent's tree is not copied", hdr.Name)
		default:
			// Devices, sockets and hard links have no meaning on this side.
			continue
		}
	}
}

func modeOr(mode, fallback os.FileMode) os.FileMode {
	if mode == 0 {
		return fallback
	}
	return mode
}

func (m podMachine) PushFile(ctx context.Context, local, file string, mode os.FileMode) error {
	if err := m.bad; err != nil {
		return err
	}
	content, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	// PushFile has no ids of its own, so it asks for none: agentd writes the
	// file as the pod's agent user, which owns it on that side.
	return m.WriteFile(ctx, file, content, 0, 0, mode)
}

// Snapshot archives the pod's claim to the master: the claim is ReadWriteOnce,
// so the daemon stops the pod, runs a helper that mounts the claim, streams a
// tar of it over the API server's exec, and starts the pod again. The whole
// machine is in the archive, so this is the machine half of a snapshot; the
// worktree's half is the git ref internal/agent keeps.
func (m podMachine) Snapshot(ctx context.Context, name string) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.Snapshot(ctx, m.target, name)
}

func (m podMachine) SnapshotList(ctx context.Context) ([]Snapshot, error) {
	if err := m.bad; err != nil {
		return nil, err
	}
	return m.pods.Snapshots(ctx, m.target)
}

func (m podMachine) RestoreSnapshot(ctx context.Context, name string) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.RestoreSnapshot(ctx, m.target, name)
}

func (m podMachine) DeleteSnapshot(ctx context.Context, name string) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.DeleteSnapshot(ctx, m.target, name)
}

func (m podMachine) HasSnapshot(ctx context.Context, name string) (bool, error) {
	if err := m.bad; err != nil {
		return false, err
	}
	return m.pods.HasSnapshot(ctx, m.target, name)
}

// PrepareClaim primes the claim of an agent that has no pod yet from a snapshot
// another agent took. It is what a fork on a cluster starts with: the archive
// carries the home and the tree, so the new pod begins as the source's machine.
func (m podMachine) PrepareClaim(ctx context.Context, claim, from string) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.PrepareClaim(ctx, m.target, claim, from)
}

// SnapshotGit records the worktree half of a snapshot in the snapshot itself,
// which is the only place it can live for a pod: the host has no ref for a
// worktree that is on a claim.
func (m podMachine) SnapshotGit(ctx context.Context, name, ref, commit, head string) error {
	if err := m.bad; err != nil {
		return err
	}
	return m.pods.SnapshotGit(ctx, m.target, name, ref, commit, head)
}

// SnapshotGitInfo reads that record back, without needing the pod to be up.
func (m podMachine) SnapshotGitInfo(ctx context.Context, name string) (string, string, error) {
	if err := m.bad; err != nil {
		return "", "", err
	}
	return m.pods.SnapshotGitInfo(ctx, m.target, name)
}

// Usage is the bytes an agent's claim holds, measured from inside its pod:
// Kubernetes says how big a claim is, not how full, and the claim is where both
// the worktree and the home live. A stopped pod has no way to be measured
// without starting one, so it says so rather than reporting its claim's size as
// if it were its use.
func (m podMachine) Usage(ctx context.Context) (int64, error) {
	if err := m.bad; err != nil {
		return 0, err
	}
	info, err := m.pods.Info(ctx, m.target)
	if err != nil {
		return 0, err
	}
	if !info.Running() {
		return 0, fmt.Errorf("%s's pod is stopped, and its claim can only be measured from inside it", m.ref)
	}
	out, err := m.Exec(ctx, "sh", "-c", `du -s -B1 /workspace /home/agent 2>/dev/null | awk '{s+=$1} END {print s+0}'`)
	if err != nil {
		return 0, fmt.Errorf("measuring %s's claim: %w", m.ref, err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("measuring %s's claim: %q is not a size", m.ref, strings.TrimSpace(out))
	}
	return size, nil
}

// Dial opens a port from inside the pod, for a desktop's VNC or a browser's
// debugging port. Frames on a dial stream are raw bytes both ways.
func (m podMachine) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	s, err := m.stream(ctx, gateway.Header{Op: gateway.OpDial, Network: network, Addr: addr})
	if err != nil {
		return nil, err
	}
	c := &podConn{
		stream:  s,
		ref:     m.ref,
		network: network,
		addr:    addr,
		done:    make(chan struct{}),
	}
	// The connection lives past this call and has no deadline to set, so its
	// context is what ends it: closing the stream is the only way to wake a
	// Read blocked on a pod that will not answer.
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-c.done:
		}
	}()
	return c, nil
}

// podConn is a connection to a port inside the pod, carried over one dial
// stream.
type podConn struct {
	stream *gateway.Stream
	ref    string
	// network and addr are what the pod dialed, kept so the addresses this
	// conn reports say where it goes.
	network string
	addr    string
	pending []byte
	done    chan struct{}
	once    sync.Once
	err     error
}

func (c *podConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(c.pending) == 0 {
		kind, payload, err := c.stream.Read()
		if err != nil {
			return 0, err
		}
		switch kind {
		case gateway.FrameData:
			if len(payload) == 0 {
				continue
			}
			c.pending = payload
		case gateway.FrameExit:
			return 0, io.EOF
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *podConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := c.stream.Write(gateway.FrameData, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *podConn) Close() error {
	c.once.Do(func() {
		close(c.done)
		c.err = c.stream.Close()
	})
	return c.err
}

// SetDeadline and its two halves do nothing: the far end of a stream has no
// deadlines, and a read that must not wait is ended by closing the conn, which
// the context above does. They return nil so net/http and websocket code that
// sets deadlines on every conn still works.
func (c *podConn) SetDeadline(time.Time) error      { return nil }
func (c *podConn) SetReadDeadline(time.Time) error  { return nil }
func (c *podConn) SetWriteDeadline(time.Time) error { return nil }

func (c *podConn) LocalAddr() net.Addr { return podAddr{c.ref, c.network, c.addr} }

func (c *podConn) RemoteAddr() net.Addr { return podAddr{c.ref, c.network, c.addr} }

// podAddr names the pod a connection goes to, and the address inside it.
// There is no local address on this side: the stream is not a socket.
type podAddr struct {
	ref     string
	network string
	addr    string
}

func (a podAddr) Network() string { return a.network }

func (a podAddr) String() string { return a.ref + " " + a.addr }

// stream opens one stream on the agent's gateway session, which is where every
// pod operation starts.
func (m podMachine) stream(ctx context.Context, h gateway.Header) (*gateway.Stream, error) {
	if err := m.bad; err != nil {
		return nil, err
	}
	return m.sessions.Dial(ctx, m.target, h)
}

// watchContext closes s when ctx ends, and returns the function that stops the
// watch. A frame stream has no deadline, so a read waiting on a pod that went
// away would otherwise wait until the whole connection noticed.
func watchContext(ctx context.Context, s *gateway.Stream) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// exitStatus is a command that ended with a status other than 0, in the error
// the package already gives for one: callers read it with incus.ExitCode
// without caring which runtime ran the command.
func exitStatus(code int) error {
	return &incus.ExitError{Code: code}
}

// quoteForShell quotes s for the sh -c command line UserExec runs, since a
// path from outside the pod can hold a space or a quote.
func quoteForShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
