package chv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/paths"
)

// How long the supervisor gives things.
const (
	guestShutdownTimeout = 60 * time.Second // the guest, to power off after the power button
	agentsStopTimeout    = 2 * time.Minute  // the daemon, to stop every agent before a stop
	childGrace           = 5 * time.Second  // passt and virtiofsd, to exit on SIGTERM
	readyPoll            = 500 * time.Millisecond
)

// supervisor is `agentbox vm run`: one process for the whole life of a VM.
type supervisor struct {
	c    Config
	l    Layout
	p    paths.Paths
	self string // this agentbox, ssh's proxy command
	log  *log.Logger
	m    machine

	policy memPolicy
	stopc  chan api.VMStopRequest // a stop asked for over vm.sock

	mu          sync.Mutex
	state       string
	since       time.Time
	daemonReady bool
	sample      *memSample
	mem         memState
}

// Supervise is `agentbox vm run`: it runs the VM in the foreground (passt,
// virtiofsd and cloud-hypervisor, or on a Mac the Virtualization framework),
// forwards its sockets to the host, sizes its
// memory, and serves its state on p.VMSocket, until the VM powers off.
func Supervise(ctx context.Context, c Config, l Layout, p paths.Paths) error {
	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stopSignals()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	s := &supervisor{
		c: c, l: l, p: p, self: self,
		log:    log.New(os.Stderr, "", log.LstdFlags),
		policy: memPolicy{Min: c.MemoryMin, Cap: c.MemoryMin + hotplugSize(c)},
		stopc:  make(chan api.VMStopRequest, 1),
		state:  api.VMStarting,
		since:  time.Now(),
	}
	s.m, err = newMachine(c, l, s.log.Writer(), s.logf)
	if err != nil {
		return err
	}
	s.mem.Requested = c.MemoryMin
	return s.run(ctx)
}

func (s *supervisor) logf(format string, args ...any) { s.log.Printf(format, args...) }

func (s *supervisor) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != state {
		s.state, s.since = state, time.Now()
	}
}

func (s *supervisor) getState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *supervisor) run(ctx context.Context) error {
	if err := os.MkdirAll(s.l.Run(), 0o700); err != nil {
		return err
	}
	unlock, err := lockFile(filepath.Join(s.l.Run(), "supervisor.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	// Holding the lock, whatever is left in Run is a dead supervisor's.
	for _, f := range s.runFiles() {
		_ = os.Remove(f)
	}
	if err := os.MkdirAll(filepath.Dir(s.p.VMSocket()), 0o700); err != nil {
		return err
	}
	vmLn, err := listenUnixOnce(s.p.VMSocket(), "the VM's supervisor")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(s.p.VMSocket()) }()
	if err := os.WriteFile(s.l.PIDFile(), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		_ = vmLn.Close()
		return err
	}
	defer func() { _ = os.Remove(s.l.PIDFile()) }()

	// The daemon's socket is taken before the VM starts: a daemon that runs
	// on this machine itself is a reason not to start at all.
	if err := os.MkdirAll(filepath.Dir(s.p.Socket()), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(s.p.Socket()), 0o700)
	daemonLn, err := listenUnixOnce(s.p.Socket(), "AgentBox's daemon")
	if err != nil {
		_ = vmLn.Close()
		return err
	}
	defer func() { _ = daemonLn.Close() }()

	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(vmLn) }()
	defer func() { _ = srv.Close() }()

	s.logf("starting VM %s: %d CPUs, %s of memory (up to %s)", s.c.Name, s.c.CPUs, gib(s.policy.Min), gib(s.policy.Cap))
	started := time.Now()
	if err := s.m.boot(ctx); err != nil {
		s.stopChildren()
		return err
	}
	s.logf("the VM is up in %s", time.Since(started).Round(time.Millisecond))

	fwdCtx, stopForwards := context.WithCancel(context.Background())
	forwards := s.forwards(fwdCtx, daemonLn)
	closeForwards := func() {
		stopForwards()
		for _, f := range forwards {
			_ = f.ln.Close()
		}
	}
	defer closeForwards()

	loops, stopLoops := context.WithCancel(ctx)
	defer stopLoops()
	go s.waitDaemon(loops, started)
	go s.memoryLoop(loops)

	var req api.VMStopRequest
	select {
	case <-s.m.done():
		stopLoops()
		closeForwards()
		s.setState(api.VMStopping)
		s.stopChildren()
		if err := s.m.exitErr(); err != nil {
			return fmt.Errorf("%v; its log: %s", err, s.l.Log())
		}
		s.logf("the VM powered off")
		return nil
	case <-ctx.Done():
		s.logf("stopping the VM: %v", context.Cause(ctx))
	case req = <-s.stopc:
		s.logf("stopping the VM, as asked (agents too: %t)", req.Agents)
	}
	stopLoops()
	s.setState(api.VMStopping)
	if req.Agents {
		s.stopAgents()
	}
	closeForwards()
	s.shutdown(true)
	return nil
}

// forwards serves the daemon's socket and its preview proxy on the host,
// from the VM.
func (s *supervisor) forwards(ctx context.Context, daemonLn net.Listener) []*forward {
	open := func() bool { return s.getState() != api.VMPaused }
	dial := func(port uint32) func(context.Context) (net.Conn, error) {
		return func(ctx context.Context) (net.Conn, error) { return dialVsock(ctx, s.l.VsockSocket(), port) }
	}
	fs := []*forward{{name: "daemon socket", ln: daemonLn, dial: dial(PortDaemon), open: open, logf: s.logf}}
	if previewLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", PortPreview)); err != nil {
		s.logf("not forwarding the preview proxy: %v", err)
	} else {
		fs = append(fs, &forward{name: "preview proxy", ln: previewLn, dial: dial(PortPreview), open: open, logf: s.logf})
	}
	for _, f := range fs {
		go f.serve(ctx)
	}
	return fs
}

// listenUnixOnce listens on socket, removing a stale one first but never one
// that something still answers on: that is who's already there.
func listenUnixOnce(socket, what string) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("%s already answers on %s", what, socket)
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, fmt.Errorf("checking %s: %w", socket, err)
	}
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(true)
	_ = os.Chmod(socket, 0o600)
	return ln, nil
}

// daemonClient is an HTTP client for the daemon in the VM, over vsock rather
// than the forwarded socket, so it works while the forwards are closed.
func (s *supervisor) daemonClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialVsock(ctx, s.l.VsockSocket(), PortDaemon)
			},
			DisableKeepAlives: true,
		},
	}
}

// waitDaemon moves the VM from starting to running once its daemon answers.
func (s *supervisor) waitDaemon(ctx context.Context, started time.Time) {
	client := s.daemonClient(5 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentbox/v1/version", nil)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				s.mu.Lock()
				s.daemonReady = true
				if s.state == api.VMStarting {
					s.state, s.since = api.VMRunning, time.Now()
				}
				s.mu.Unlock()
				s.logf("the daemon in the VM answers, %s after starting", time.Since(started).Round(time.Millisecond))
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(readyPoll):
		}
	}
}

// stopAgents stops every running agent through the daemon, so none restarts
// with the VM: Incus starts again whatever was running when it stopped.
func (s *supervisor) stopAgents() {
	ctx, cancel := context.WithTimeout(context.Background(), agentsStopTimeout)
	defer cancel()
	client := s.daemonClient(agentsStopTimeout)
	var agents []api.Agent
	if err := daemonJSON(ctx, client, http.MethodGet, "/v1/agents", &agents); err != nil {
		s.logf("not stopping the agents: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, a := range agents {
		if a.State != "running" && a.State != "paused" {
			continue
		}
		wg.Go(func() {
			path := "/v1/agents/" + a.Project + "/" + a.Name + "/stop"
			if err := daemonJSON(ctx, client, http.MethodPost, path, nil); err != nil {
				s.logf("stopping agent %s: %v", a.Ref, err)
				return
			}
			s.logf("stopped agent %s", a.Ref)
		})
	}
	wg.Wait()
}

func daemonJSON(ctx context.Context, client *http.Client, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://agentbox"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
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
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// shutdown powers the VM off, the guest's own way when graceful (the power
// button, and up to guestShutdownTimeout for it), and then ends everything
// that ran it.
func (s *supervisor) shutdown(graceful bool) {
	started := time.Now()
	if graceful && s.m.running() {
		if off, err := s.m.powerOff(guestShutdownTimeout); err != nil {
			s.logf("pressing the VM's power button: %v", err)
		} else if off {
			s.logf("the VM powered off in %s", time.Since(started).Round(time.Millisecond))
		} else {
			s.logf("the VM didn't power off within %s: ending it", guestShutdownTimeout)
		}
	}
	s.stopChildren()
}

// runFiles are the sockets the VM runs with, and what its programs leave
// beside them.
func (s *supervisor) runFiles() []string {
	return append(s.m.runFiles(), s.l.ControlSocket())
}

func (s *supervisor) stopChildren() {
	s.m.halt()
	for _, f := range s.runFiles() {
		_ = os.Remove(f)
	}
}

// memoryLoop sizes the VM's memory (memory.go) every memTick, while it runs.
func (s *supervisor) memoryLoop(ctx context.Context) {
	tick := time.NewTicker(memTick)
	defer tick.Stop()
	movable := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if s.getState() == api.VMPaused {
			continue
		}
		sample, err := s.sampleMemory(ctx)
		if err != nil {
			continue // not booted yet, or ssh is down: leave it as it is
		}
		if !movable && s.m.balloon() == 0 {
			movable = s.ensureMovable(ctx, sample)
		}
		sample.Resident, _ = s.m.resident()
		s.mu.Lock()
		s.sample = &sample
		d := s.policy.decide(&s.mem, &sample, time.Now())
		s.mu.Unlock()
		if d.Reclaim {
			started := time.Now()
			if _, err := runSSHFor(ctx, s.c, s.l, s.self, reclaimScript, 30*time.Second); err != nil {
				s.logf("reclaiming the VM's caches: %v", err)
			} else if d.Target == 0 {
				s.logf("memory: dropped the VM's caches (%s), in %s", d.Reason, time.Since(started).Round(time.Millisecond))
			}
		}
		if d.Target == 0 {
			continue
		}
		started := time.Now()
		if err := s.m.setMemory(ctx, d.Target); err != nil {
			s.logf("resizing the VM's memory to %s: %v", gib(d.Target), err)
			continue
		}
		s.logf("memory: %s → %s (%s), asked in %s", gib(sample.Total), gib(d.Target), d.Reason, time.Since(started).Round(time.Millisecond))
	}
}

// reclaimScript drops the guest's page cache and the kernel's reclaimable
// caches (dentries, inodes), then compacts its free memory into whole blocks,
// which is what free page reporting hands back to the host.
const reclaimScript = "sync; echo 3 | sudo -n tee /proc/sys/vm/drop_caches >/dev/null; echo 1 | sudo -n tee /proc/sys/vm/compact_memory >/dev/null"

// ensureMovable makes the guest online the memory it's given as movable,
// which is what lets it unplug that memory again: memory onlined as normal
// soon holds pages the kernel can't move. The VM's kernel command line says
// so from its second boot (image.go); this covers the first.
func (s *supervisor) ensureMovable(ctx context.Context, sample memSample) bool {
	switch sample.AutoOnline {
	case "online_movable", "":
		return true
	case "online", "online_kernel":
		// memory_hotplug.online_policy=auto-movable onlines movable
		// itself, as long as it's safe to: leave it to it.
		out, _ := s.ssh(ctx, "cat /sys/module/memory_hotplug/parameters/online_policy 2>/dev/null")
		if firstLine(out) == "auto-movable" {
			return true
		}
	}
	if _, err := s.ssh(ctx, "echo online_movable | sudo -n tee /sys/devices/system/memory/auto_online_blocks >/dev/null"); err != nil {
		s.logf("making the VM online its memory as movable: %v", err)
		return false
	}
	s.logf("the VM now onlines the memory it's given as movable (it was %q)", sample.AutoOnline)
	return true
}

func (s *supervisor) sampleMemory(ctx context.Context) (memSample, error) {
	booted := s.m.balloon()
	script := memScript
	if booted > 0 {
		script += "\n" + balloonScript
	}
	out, err := s.ssh(ctx, script)
	if err != nil {
		return memSample{}, err
	}
	sample, err := parseMemSample(out)
	if err == nil && booted > 0 {
		sample = withBalloon(sample, out, booted)
	}
	return sample, err
}

// ssh runs a shell command in the VM, through the ControlMaster every
// command shares.
func (s *supervisor) ssh(ctx context.Context, script string) (string, error) {
	return runSSH(ctx, s.c, s.l, s.self, script)
}
