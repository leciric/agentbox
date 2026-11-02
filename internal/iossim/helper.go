package iossim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// HelperCommand is the agentbox command that is the helper: cmd/agentbox
// runs Serve for it, on the Mac, without the VM.
const HelperCommand = "ios-helper"

// ServeOptions are what the helper is started with.
type ServeOptions struct {
	ConfigDir string   // the Mac's AgentBox config: the state file goes there
	Roots     []string // where apps may be installed from
	Version   string
	Log       io.Writer
	// Runner and Launcher default to the real xcrun and simhost.
	Runner   Runner
	Launcher Launcher
	// Addr defaults to 127.0.0.1:0.
	Addr string
}

// Serve runs the helper until ctx ends. A second one finds the first holding
// the lock and returns nil: there is only ever one.
func Serve(ctx context.Context, o ServeOptions) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Runner == nil {
		o.Runner = Exec
	}
	simctl := Simctl{Run: o.Runner}
	if o.Launcher == nil {
		o.Launcher = SimhostLauncher(filepath.Join(o.ConfigDir, "simhost"), simctl, o.Log)
	}
	if o.Addr == "" {
		o.Addr = "127.0.0.1:0"
	}
	if err := os.MkdirAll(o.ConfigDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(o.ConfigDir, "ios-helper.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_, _ = fmt.Fprintln(o.Log, "another ios-helper runs")
		return nil
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return err
	}
	srv := NewServer(newToken(), o.Version, simctl, o.Roots, o.Launcher)
	file := StateFile(o.ConfigDir)
	st := State{Port: ln.Addr().(*net.TCPAddr).Port, Token: srv.Token, PID: os.Getpid(), Version: o.Version}
	if err := writeState(file, st); err != nil {
		_ = ln.Close()
		return err
	}
	defer func() {
		if cur, err := ReadState(file); err == nil && cur.PID == st.PID {
			_ = os.Remove(file)
		}
	}()
	_, _ = fmt.Fprintf(o.Log, "ios-helper %s on %s\n", o.Version, ln.Addr())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go srv.Run(ctx)
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = hs.Shutdown(shut)
	}()
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// retryAfter is how long Ensure leaves a helper that didn't start before it
// tries again.
const retryAfter = 10 * time.Minute

// EnsureOptions say how the front end starts the helper.
type EnsureOptions struct {
	ConfigDir string
	Roots     []string
	Version   string
	// Exe is the agentbox binary; Start runs it (tests replace it).
	Exe   string
	Start func(cmd *exec.Cmd) error
	// Wait is how long to wait for a new helper to answer.
	Wait time.Duration
}

// Ensure makes sure the helper runs, the same version as this agentbox,
// starting it in the background, outliving this command, when it doesn't:
// an update replaces the one an older agentbox started.
func Ensure(ctx context.Context, o EnsureOptions) error {
	file := StateFile(o.ConfigDir)
	if st, err := ReadState(file); err == nil {
		v, err := local(file).Version(ctx)
		if err == nil && v == o.Version {
			return nil
		}
		if err == nil && st.PID > 0 {
			stopHelper(st.PID)
			waitGone(ctx, file, st.PID)
		}
	}
	// One that didn't start lately isn't tried again on every command, each
	// of which would wait for it.
	failed := filepath.Join(o.ConfigDir, "ios-helper.failed")
	if fi, err := os.Stat(failed); err == nil && time.Since(fi.ModTime()) < retryAfter {
		return fmt.Errorf("the iOS Simulator helper didn't start at %s: see ios-helper.log in AgentBox's config", fi.ModTime().Format(time.Kitchen))
	}
	args := []string{HelperCommand, "--config", o.ConfigDir}
	for _, r := range o.Roots {
		args = append(args, "--root", r)
	}
	cmd := exec.Command(o.Exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if logf, err := os.OpenFile(filepath.Join(o.ConfigDir, "ios-helper.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		cmd.Stdout, cmd.Stderr = logf, logf
		defer func() { _ = logf.Close() }()
	}
	start := o.Start
	if start == nil {
		start = func(cmd *exec.Cmd) error {
			if err := cmd.Start(); err != nil {
				return err
			}
			return cmd.Process.Release()
		}
	}
	if err := os.MkdirAll(o.ConfigDir, 0o700); err != nil {
		return err
	}
	if err := start(cmd); err != nil {
		return fmt.Errorf("starting the iOS Simulator helper: %w", err)
	}
	wait := o.Wait
	if wait == 0 {
		wait = 3 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if v, err := local(file).Version(ctx); err == nil && v == o.Version {
			_ = os.Remove(failed)
			return nil
		}
		if time.Now().After(deadline) {
			_ = os.WriteFile(failed, nil, 0o600)
			return errors.New("the iOS Simulator helper didn't start: see ios-helper.log in AgentBox's config")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// stopHelper asks an older helper to stop. A variable for tests, whose
// helper is the test's own process.
var stopHelper = func(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
}

// local is a client of the helper from the Mac itself.
func local(file string) *Client {
	c := NewClient(file)
	c.HTTP.Timeout = 2 * time.Second
	return &Client{File: file, HTTP: c.HTTP, host: "127.0.0.1"}
}

// waitGone waits a little for an old helper to give up the state file.
func waitGone(ctx context.Context, file string, pid int) {
	for range 30 {
		if st, err := ReadState(file); err != nil || st.PID != pid {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
