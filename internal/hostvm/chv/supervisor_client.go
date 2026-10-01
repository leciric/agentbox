package chv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/paths"
)

// The front end's side of the supervisor: what `agentbox vm start`, `stop`,
// `pause`, `resume` and `status` do, through paths.VMSocket.

const (
	startTimeout   = 5 * time.Minute // from starting the supervisor to the VM answering ssh
	stopTimeout    = 2 * time.Minute // for the supervisor to exit, on top of stopping the agents
	sshTryTimeout  = 15 * time.Second
	statusDeadline = 3 * time.Second
)

// vmSocketWait is how long a supervisor has to answer on its socket, once
// started.
var vmSocketWait = 30 * time.Second

// ErrNotRunning is an action on a VM that isn't running.
var ErrNotRunning = errors.New("AgentBox's VM isn't running: agentbox vm start")

// errInVM is starting the VM from inside it. Its HOME can be the host's, whose
// VM it would then see: a supervisor there would take its lock (virtiofs's
// locks are the VM's own, the host's supervisor doesn't hold them there),
// remove the running VM's sockets on the share, and fail to start another.
var errInVM = errors.New("this is AgentBox's VM: it's started from the host, not from in here")

// Start starts the supervisor in the background, detached from this process,
// and waits until the VM answers ssh. A VM that runs already is left alone;
// a paused one is resumed.
func Start(ctx context.Context, c Config, l Layout, p paths.Paths, log io.Writer) error {
	if hostos.InVM() {
		return errInVM
	}
	started := time.Now()
	// A supervisor that holds its lock runs the VM, whether or not it answers:
	// one only starting answers in a moment; one whose socket is gone never
	// will, and starting another would only fail on its lock, having removed
	// nothing, but say nothing of why.
	for {
		if _, err := vmStatus(ctx, p); err == nil {
			break
		}
		pid, running := supervisorRunning(l)
		if !running {
			break
		}
		if time.Since(started) > vmSocketWait {
			return startFailed(l, lostSupervisor(pid, p))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if st, err := vmStatus(ctx, p); err == nil {
		switch st.State {
		case api.VMPaused:
			_, _ = fmt.Fprintln(log, "Resuming AgentBox's VM…")
			return Resume(ctx, l, p)
		case api.VMStopping:
			_, _ = fmt.Fprintln(log, "Waiting for AgentBox's VM to finish stopping…")
			if err := waitExit(ctx, l, stopTimeout+agentsStopTimeout); err != nil {
				return err
			}
		default:
			return waitSSH(ctx, c, l, log, nil, started)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.Dir(), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(l.Log(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	cmd := exec.Command(self, "vm", "run")
	cmd.Env = append(os.Environ(), "AGENTBOX_VM="+c.Name)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = detachedAttr()
	_, _ = fmt.Fprintln(log, "Starting AgentBox's VM…")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the VM's supervisor: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	for {
		if _, err := vmStatus(ctx, p); err == nil {
			break
		}
		select {
		case <-exited:
			return startFailed(l, "the VM's supervisor exited")
		case <-ctx.Done():
			return startFailed(l, "the VM's supervisor didn't start")
		case <-time.After(100 * time.Millisecond):
		}
		if time.Since(started) > vmSocketWait {
			return startFailed(l, "the VM's supervisor didn't start")
		}
	}
	return waitSSH(ctx, c, l, log, exited, started)
}

// waitSSH waits for the VM to answer ssh.
func waitSSH(ctx context.Context, c Config, l Layout, log io.Writer, exited <-chan struct{}, started time.Time) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	for {
		if _, err := runSSH(ctx, c, l, self, "true"); err == nil {
			_, _ = fmt.Fprintf(log, "AgentBox's VM is up (%s).\n", time.Since(started).Round(100*time.Millisecond))
			return nil
		}
		select {
		case <-exited:
			return startFailed(l, "the VM stopped while it was starting")
		case <-ctx.Done():
			return startFailed(l, "the VM didn't answer ssh in "+startTimeout.String())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// startFailed is why the VM didn't start, with the end of its logs.
func startFailed(l Layout, why string) error {
	var b strings.Builder
	b.WriteString(why)
	if t := tail(l.Log(), 15); t != "" {
		fmt.Fprintf(&b, "\n\n%s:\n%s", l.Log(), t)
	}
	if t := tail(l.SerialLog(), 15); t != "" {
		fmt.Fprintf(&b, "\n\n%s:\n%s", l.SerialLog(), t)
	}
	return errors.New(b.String())
}

// runSSH runs a shell script in the VM, and returns what it printed.
func runSSH(ctx context.Context, c Config, l Layout, self, script string) (string, error) {
	return runSSHFor(ctx, c, l, self, script, sshTryTimeout)
}

// runSSHFor is runSSH for a script that may take longer than a probe.
func runSSHFor(ctx context.Context, c Config, l Layout, self, script string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := SSHArgs(c, l, self, "", false, []string{"sh", "-c", script})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// The ControlMaster ssh leaves in the background keeps its proxy
	// command, and the proxy command keeps ssh's stderr: don't wait for it.
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, msg)
		}
		return string(out), err
	}
	return string(out), nil
}

// Stop powers the VM off and waits for its supervisor to exit. With agents
// set, every running agent is stopped through the daemon first.
func Stop(ctx context.Context, c Config, l Layout, p paths.Paths, agents bool, log io.Writer) error {
	started := time.Now()
	err := vmPost(ctx, p, "/v1/vm/stop", api.VMStopRequest{Agents: agents})
	if err != nil {
		pid, alive := supervisorRunning(l)
		if !alive {
			_, _ = fmt.Fprintln(log, "AgentBox's VM isn't running.")
			return nil
		}
		if pid == 0 {
			return fmt.Errorf("AgentBox's VM's supervisor holds %s, but can't be found to stop it", l.LockFile())
		}
		// A supervisor that doesn't answer yet (or any more) stops the
		// same way on SIGTERM, only without stopping the agents.
		if err := terminate(pid); err != nil {
			return fmt.Errorf("stopping the VM's supervisor: %w", err)
		}
	}
	if agents {
		_, _ = fmt.Fprintln(log, "Stopping the agents and AgentBox's VM…")
	} else {
		_, _ = fmt.Fprintln(log, "Stopping AgentBox's VM…")
	}
	timeout := stopTimeout
	if agents {
		timeout += agentsStopTimeout
	}
	if err := waitExit(ctx, l, timeout); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "AgentBox's VM is off (%s).\n", time.Since(started).Round(100*time.Millisecond))
	return nil
}

// waitExit waits for the supervisor to exit.
func waitExit(ctx context.Context, l Layout, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, alive := supervisorRunning(l); !alive {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the VM's supervisor didn't exit in %s; see %s", timeout, l.Log())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// supervisorPID is the supervisor's pid, from its pid file, and whether it
// still runs.
func supervisorPID(l Layout) (int, bool) {
	b, err := os.ReadFile(l.PIDFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, supervisorAlive(pid)
}

// supervisorRunning is whether a supervisor runs the VM, and its pid (0 if it
// can't be found): the pid file's, or else whoever holds its lock, which is
// what really says so. The pid file goes with the sockets beside it, when
// something removes them.
func supervisorRunning(l Layout) (int, bool) {
	if pid, alive := supervisorPID(l); alive {
		return pid, true
	}
	unlock, err := lockFile(l.LockFile())
	if err == nil {
		unlock()
		return 0, false
	}
	if !errors.Is(err, errLocked) {
		return 0, false
	}
	return lockHolder(l.LockFile()), true
}

// lostSupervisor is why a VM whose supervisor runs can't be used.
func lostSupervisor(pid int, p paths.Paths) string {
	who := "its supervisor"
	if pid > 0 {
		who = fmt.Sprintf("its supervisor is pid %d", pid)
	}
	return fmt.Sprintf("AgentBox's VM runs (%s), but doesn't answer on %s: something removed its sockets. agentbox vm stop ends it, and agentbox vm start starts it again", who, p.VMSocket())
}

func Pause(ctx context.Context, l Layout, p paths.Paths) error {
	return vmPost(ctx, p, "/v1/vm/pause", nil)
}

func Resume(ctx context.Context, l Layout, p paths.Paths) error {
	return vmPost(ctx, p, "/v1/vm/resume", nil)
}

// Resize has the running VM's supervisor give it req's CPUs and memory cap
// without a restart; it fails when they don't fit in VMStatus.Live.
func Resize(ctx context.Context, l Layout, p paths.Paths, req api.VMResizeRequest) error {
	return vmPost(ctx, p, "/v1/vm/resize", req)
}

// Status is the VM's state; it never fails, a VM it can't reach is off.
func Status(ctx context.Context, c Config, l Layout, p paths.Paths) api.VMStatus {
	name := c.Name
	if name == "" {
		name = DefaultName
	}
	if !Exists(p, name) {
		return api.VMStatus{
			Mode: api.ModeVM, Driver: api.VMDriverCloudHypervisor, Name: name,
			State: api.VMMissing, Problem: ErrNotCreated.Error(),
		}
	}
	if st, err := vmStatus(ctx, p); err == nil {
		st.Disk = diskStatus(l)
		return st
	}
	st := offStatus(c, l)
	if pid, running := supervisorRunning(l); running {
		// Not off, and not to be started again: it runs, unreachable.
		st.State = api.VMStarting
		st.Problem = lostSupervisor(pid, p)
	}
	return st
}

// vmStatus asks the supervisor for the VM's state, all but its disk, which
// Status measures itself. The supervisor runs as long as the VM does, through
// an upgrade of the front end, so it may be older and say less: one from
// before #161 sent its disk as {size, used}, which decodes into api.VMDisk
// with nothing allocated, and one from #161 sent its pool as a number, which
// fails to decode at all and made a running VM look lost.
func vmStatus(ctx context.Context, p paths.Paths) (api.VMStatus, error) {
	var st struct {
		api.VMStatus
		Disk json.RawMessage `json:"disk"`
	}
	err := vmRequest(ctx, p, http.MethodGet, "/v1/vm", nil, &st, statusDeadline)
	return st.VMStatus, err
}

func vmPost(ctx context.Context, p paths.Paths, path string, body any) error {
	return vmRequest(ctx, p, http.MethodPost, path, body, nil, 30*time.Second)
}

func vmRequest(ctx context.Context, p paths.Paths, method, path string, body, out any, timeout time.Duration) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agentbox-vm"+path, r)
	if err != nil {
		return err
	}
	client := unixHTTPClient(p.VMSocket(), timeout)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return ErrNotRunning
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var e api.Error
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
