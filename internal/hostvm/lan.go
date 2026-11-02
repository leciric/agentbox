package hostvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/lan"
)

// Phones on the local network reach the daemon in Lima's VM through the Mac:
// Lima's network is the VM's own, and what it forwards it forwards to the
// Mac's localhost. `agentbox vm lan` is what does it (lan.Forward): it opens
// phones' port on the Mac while the daemon has them on, and passes their
// requests on over the socket Lima forwards. Lima has nothing of ours running
// beside the VM, so whatever brings the VM up (Ready: `vm start`, and every
// command forwarded to the VM, the app's `daemon start` among them) starts it
// in the background when it isn't running, and it ends once the VM has
// stopped. A VM of package chv has its supervisor do the same.
//
// macOS's application firewall, when it's on, asks the user the first time
// agentbox accepts a connection whether it may, and turns phones away when
// told not to: macFirewall says so, and what to allow. Local network privacy
// (macOS 15) is about connections a Mac's apps make to the network, which
// this doesn't: it only accepts them.

// lanCheckEvery is how often `vm lan` looks at whether the VM still runs,
// when the daemon doesn't answer. A variable for tests.
var lanCheckEvery = 30 * time.Second

// lanLock is held by the `vm lan` that runs, one per VM.
func (v *VM) lanLock() string { return filepath.Join(v.Paths.Config, "vm", v.Name+".lan.lock") }

// lanLog is where `vm lan` says what it did.
func (v *VM) lanLog() string { return filepath.Join(v.Paths.Data, "vm-lan.log") }

// startLAN starts `agentbox vm lan` in the background, for Lima's VM, unless
// it runs already. A variable for tests, where the executable is the test's.
var startLAN = func(v *VM) error {
	if v.CHV != nil || lanRunning(v.lanLock()) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(v.Paths.Data, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(v.lanLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(exe, "vm", "lan")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// lanRunning reports whether a `vm lan` holds lock.
func lanRunning(lock string) bool {
	f, err := os.Open(lock)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// RunLAN is `agentbox vm lan`: phones' port on this computer, for the daemon
// in Lima's VM, until the VM stops or ctx ends. It returns at once when
// another one runs.
func (v *VM) RunLAN(ctx context.Context) error {
	if v.CHV != nil {
		return errors.New("this VM's supervisor opens phones' port itself")
	}
	if err := os.MkdirAll(filepath.Dir(v.lanLock()), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(v.lanLock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logf := func(format string, a ...any) {
		_, _ = fmt.Fprintf(v.Log, "%s %s\n", time.Now().Format(time.DateTime), fmt.Sprintf(format, a...))
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", v.Paths.Socket())
	}
	fwd := lan.NewForward(dial, logf)
	if runtime.GOOS == "darwin" {
		if exe, err := os.Executable(); err == nil {
			fwd.Firewall = func(ctx context.Context, _ int) string { return macFirewall(ctx, exe) }
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(lanCheckEvery):
			}
			if c, err := dial(ctx); err == nil {
				_ = c.Close()
				continue
			}
			if st, err := v.State(ctx); err == nil && (!st.Exists || st.Status != "Running") {
				logf("phones: the VM has stopped")
				cancel()
				return
			}
		}
	}()
	logf("phones: watching for the daemon to turn them on")
	fwd.Run(ctx)
	return nil
}

// socketfilterfw is macOS's application firewall's command.
var socketfilterfw = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// macFirewall is what to say when macOS's application firewall would turn
// phones away from exe, or "" when it wouldn't, or can't say.
func macFirewall(ctx context.Context, exe string) string {
	ask := func(args ...string) string {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, socketfilterfw, args...).Output()
		return string(out)
	}
	return firewallWarning(ask("--getglobalstate"), ask("--getblockall"), ask("--getallowsigned"), ask("--getappblocked", exe), exe)
}

// firewallWarning reads what socketfilterfw said: its global state, whether
// it blocks everything, whether it lets signed software in, and what of exe.
func firewallWarning(global, blockAll, signed, app, exe string) string {
	global, blockAll, signed, app = strings.ToLower(global), strings.ToLower(blockAll), strings.ToLower(signed), strings.ToLower(app)
	const settings = "System Settings → Network → Firewall → Options"
	switch {
	case !strings.Contains(global, "enabled") && !strings.Contains(global, "state = 1") && !strings.Contains(global, "state = 2"):
		return "" // off, or it didn't say
	case strings.Contains(global, "state = 2") || strings.Contains(blockAll, "set to block all") ||
		strings.Contains(blockAll, "enabled") && !strings.Contains(blockAll, "disabled"):
		return fmt.Sprintf("macOS's firewall blocks all incoming connections, phones' too: in %s, turn off Block all incoming connections, and allow agentbox (%s)", settings, exe)
	case strings.Contains(app, "block") && !strings.Contains(app, "not block"):
		return fmt.Sprintf("macOS's firewall blocks phones: in %s, set agentbox (%s) to Allow incoming connections", settings, exe)
	case strings.Contains(app, "permit") || strings.Contains(app, "allow"):
		return ""
	case strings.Contains(signed, "downloaded signed software enabled"):
		return "" // AgentBox's agentbox is signed, and let in without asking
	}
	return fmt.Sprintf("macOS may ask whether agentbox may accept incoming network connections: allow it, or add agentbox (%s) in %s", exe, settings)
}
