package chv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"agentbox/internal/api"
)

// chMachine runs the VM with Cloud Hypervisor, passt and virtiofsd, each a
// child of the supervisor, on Linux.
type chMachine struct {
	c    Config
	l    Layout
	log  io.Writer
	logf func(string, ...any)
	ch   *chClient
	room Room // how far it can be resized without a restart

	cloud, passt, fs *child
}

// boot starts virtiofsd, passt and Cloud Hypervisor, and waits for Cloud
// Hypervisor's API to answer.
func (m *chMachine) boot(ctx context.Context) error {
	if m.c.Home != "" {
		// virtiofsd takes as many files as it may: the hard limit, or a
		// million. What the VM keeps cached of the home costs one each.
		if n := maxOpenFiles(); n > 0 && n < minVirtiofsdFiles {
			m.logf("warning: this session may only open %d files (ulimit -Hn), and virtiofsd needs one for every file of your home the VM has in its cache: reads there may fail with \"Too many open files in system\". Raise it to %d or more (DefaultLimitNOFILE in systemd's user.conf).", n, minVirtiofsdFiles)
		}
		var err error
		m.fs, err = m.startVirtiofsd(ctx, "namespace")
		if err != nil {
			m.logf("virtiofsd can't sandbox itself in namespaces here (%v): sharing the home directory without its sandbox", err)
			if m.fs, err = m.startVirtiofsd(ctx, "none"); err != nil {
				return err
			}
		}
	}
	var err error
	if m.passt, err = startChild("passt", m.l.Bin("passt"), passtArgs(m.l), m.log); err != nil {
		return err
	}
	if err := waitSocket(ctx, m.passt, m.l.PasstSocket()); err != nil {
		return err
	}
	if m.cloud, err = startChild("cloud-hypervisor", m.l.Bin("cloud-hypervisor"), chArgs(m.c, m.l, m.room), m.log); err != nil {
		return err
	}
	if err := waitSocket(ctx, m.cloud, m.l.APISocket()); err != nil {
		return err
	}
	for range 50 {
		if err := m.ch.Ping(ctx); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("cloud-hypervisor's API didn't answer")
}

func (m *chMachine) startVirtiofsd(ctx context.Context, sandbox string) (*child, error) {
	c, err := startChild("virtiofsd", m.l.Bin("virtiofsd"), virtiofsdArgs(m.c, m.l, sandbox), m.log)
	if err != nil {
		return nil, err
	}
	if err := waitSocket(ctx, c, m.l.FSSocket()); err != nil {
		c.stop(childGrace)
		return nil, err
	}
	return c, nil
}

// waitSocket waits for a child to make its socket. It only looks for the
// file: passt and virtiofsd serve one client, and exit when it leaves, so
// connecting to see whether they answer would end them.
func waitSocket(ctx context.Context, c *child, socket string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			time.Sleep(20 * time.Millisecond) // from bind to listen
			return nil
		}
		select {
		case <-c.done:
			return fmt.Errorf("%s exited: %v", c.name, c.err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s didn't make %s", c.name, socket)
}

func (m *chMachine) done() <-chan struct{} { return m.cloud.done }

func (m *chMachine) exitErr() error {
	if m.cloud.err != nil {
		return fmt.Errorf("cloud-hypervisor exited: %v", m.cloud.err)
	}
	return nil
}

func (m *chMachine) running() bool { return m.cloud != nil && !m.cloud.exited() }

func (m *chMachine) pause(ctx context.Context) error  { return m.ch.Pause(ctx) }
func (m *chMachine) resume(ctx context.Context) error { return m.ch.Resume(ctx) }

func (m *chMachine) powerOff(timeout time.Duration) (bool, error) {
	ctx := context.Background()
	if info, err := m.ch.Info(ctx); err == nil && info.State == chPaused {
		_ = m.ch.Resume(ctx)
	}
	if err := m.ch.PowerButton(ctx); err != nil {
		return false, err
	}
	return m.waitPoweroff(timeout), nil
}

// waitPoweroff waits for the guest to power off, and for Cloud Hypervisor to
// exit with it.
func (m *chMachine) waitPoweroff(timeout time.Duration) bool {
	deadline := time.After(timeout)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-m.cloud.done:
			return true
		case <-deadline:
			return false
		case <-tick.C:
			// Cloud Hypervisor stays up with a guest that's shut down only
			// if it was told to; end it all the same.
			if info, err := m.ch.Info(context.Background()); err == nil && info.State == chShutdown {
				_ = m.ch.ShutdownVMM(context.Background())
			}
		}
	}
}

func (m *chMachine) halt() {
	if m.running() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = m.ch.ShutdownVMM(ctx)
		cancel()
		select {
		case <-m.cloud.done:
		case <-time.After(3 * time.Second):
		}
	}
	m.cloud.stop(childGrace)
	m.passt.stop(childGrace)
	m.fs.stop(childGrace)
}

func (m *chMachine) setCPUs(ctx context.Context, n int) error { return m.ch.ResizeCPUs(ctx, n) }

func (m *chMachine) setMemory(ctx context.Context, bytes int64) error {
	return m.ch.Resize(ctx, bytes)
}

// granted is what's plugged, which lags what was asked for while the guest
// plugs or unplugs it.
func (m *chMachine) granted(ctx context.Context) int64 {
	if !m.running() {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if info, err := m.ch.Info(ctx); err == nil {
		return info.MemoryActualSize
	}
	return 0
}

func (m *chMachine) resident() (vm, all int64) {
	if m.running() {
		vm = resident(m.cloud.pid())
	}
	for _, c := range []*child{m.cloud, m.passt, m.fs} {
		if c != nil && !c.exited() {
			all += resident(c.pid())
		}
	}
	return vm, all
}

// live is what the running VM can be resized to: every vCPU it booted with
// room for, and its virtio-mem region.
func (m *chMachine) live(c Config) api.VMLimits {
	return api.VMLimits{MinCPUs: 1, MaxCPUs: m.room.CPUs, MinMemory: c.MemoryMin, MaxMemory: c.MemoryMin + regionSize(c, m.room)}
}

func (m *chMachine) balloon() int64 { return 0 }

func (m *chMachine) runFiles() []string {
	return []string{
		m.l.APISocket(), m.l.APISocket() + ".lock",
		m.l.VsockSocket(),
		m.l.PasstSocket(), m.l.PasstSocket() + ".repair",
		m.l.FSSocket(), m.l.FSSocket() + ".pid",
	}
}
