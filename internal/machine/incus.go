package machine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"

	"agentbox/internal/incus"
)

// Incus is the runtime an AgentBox host runs its agents on: one instance per
// agent, copied from the base image or a project's saved base.
type Incus struct {
	Client incus.Client
	// Pool is the storage pool the machines' disks live in; "" is "default",
	// the pool AgentBox creates.
	Pool string
}

// NewIncus is the Incus runtime over c.
func NewIncus(c incus.Client) *Incus { return &Incus{Client: c} }

func (r *Incus) Kind() Kind { return KindIncus }

func (r *Incus) Machine(ref string) Machine {
	return IncusMachine{client: r.Client, pool: r.Pool, ref: ref}
}

// IncusMachine is one agent's Incus instance. Every method forwards to
// internal/incus, so the Incus path keeps behaving exactly as it did before
// the runtime seam existed.
type IncusMachine struct {
	client incus.Client
	pool   string
	ref    string
}

func (m IncusMachine) Ref() string { return m.ref }

func (m IncusMachine) Kind() Kind { return KindIncus }

func (m IncusMachine) poolName() string {
	if m.pool == "" {
		return "default"
	}
	return m.pool
}

func (m IncusMachine) Info(ctx context.Context) (Info, error) {
	inst, err := m.client.Instance(ctx, m.ref)
	return info(inst), notFound(err)
}

func (m IncusMachine) WaitReady(ctx context.Context, timeout time.Duration) (Info, error) {
	inst, err := m.client.WaitReady(ctx, m.ref, timeout)
	return info(inst), notFound(err)
}

func (m IncusMachine) Start(ctx context.Context) error { return m.client.Start(ctx, m.ref) }

func (m IncusMachine) Stop(ctx context.Context) error { return m.client.Stop(ctx, m.ref) }

func (m IncusMachine) StopWithin(ctx context.Context, timeout time.Duration) error {
	return m.client.StopWithin(ctx, m.ref, timeout)
}

func (m IncusMachine) ForceStop(ctx context.Context) error { return m.client.ForceStop(ctx, m.ref) }

func (m IncusMachine) Pause(ctx context.Context) error { return m.client.Pause(ctx, m.ref) }

func (m IncusMachine) Resume(ctx context.Context) error { return m.client.Resume(ctx, m.ref) }

func (m IncusMachine) Destroy(ctx context.Context) error { return m.client.Delete(ctx, m.ref) }

func (m IncusMachine) Exec(ctx context.Context, command ...string) (string, error) {
	return m.client.Exec(ctx, m.ref, command...)
}

func (m IncusMachine) ExecInput(ctx context.Context, stdin io.Reader, command ...string) (string, error) {
	return m.client.ExecInput(ctx, stdin, m.ref, command...)
}

func (m IncusMachine) UserExec(ctx context.Context, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	return m.client.UserExec(ctx, m.ref, user, command, stdin, stdout, stderr)
}

// Attach runs opts.Argv under `incus exec -t`, which gives it a terminal
// inside the instance, and gives this side one for the WebSocket to pass
// through.
func (m IncusMachine) Attach(ctx context.Context, opts AttachOptions) (Terminal, error) {
	argv := append([]string{"exec", m.ref, "-t", "--"}, opts.Argv...)
	cmd := exec.CommandContext(ctx, m.client.Path(), argv...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		return nil, err
	}
	return &incusTerminal{File: ptmx, cmd: cmd}, nil
}

// incusTerminal is a local pseudo-terminal driving `incus exec -t`: closing it
// ends the command, and waits for it, so nothing is left behind.
type incusTerminal struct {
	*os.File
	cmd  *exec.Cmd
	once sync.Once
}

func (t *incusTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.File, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *incusTerminal) Close() error {
	err := t.File.Close()
	t.once.Do(func() { _ = t.cmd.Wait() })
	return err
}

func (m IncusMachine) WriteFile(ctx context.Context, path string, content []byte, uid, gid int, mode os.FileMode) error {
	return m.client.WriteFile(ctx, m.ref, path, content, uid, gid, mode)
}

func (m IncusMachine) PullFile(ctx context.Context, file, target string) error {
	return m.client.PullFile(ctx, m.ref, file, target)
}

func (m IncusMachine) PullDir(ctx context.Context, from, dir string) error {
	return m.client.PullDir(ctx, m.ref, from, dir)
}

func (m IncusMachine) PushFile(ctx context.Context, local, file string, mode os.FileMode) error {
	return m.client.PushFile(ctx, local, m.ref, file, mode)
}

func (m IncusMachine) Snapshot(ctx context.Context, name string) error {
	return m.client.CreateSnapshot(ctx, m.ref, name)
}

func (m IncusMachine) SnapshotList(ctx context.Context) ([]Snapshot, error) {
	listed, err := m.client.Snapshots(ctx, m.ref)
	if err != nil {
		return nil, err
	}
	out := make([]Snapshot, 0, len(listed))
	for _, s := range listed {
		out = append(out, Snapshot{Name: s.Name, CreatedAt: s.CreatedAt})
	}
	return out, nil
}

func (m IncusMachine) RestoreSnapshot(ctx context.Context, name string) error {
	return m.client.RestoreSnapshot(ctx, m.ref, name)
}

func (m IncusMachine) DeleteSnapshot(ctx context.Context, name string) error {
	return m.client.DeleteSnapshot(ctx, m.ref, name)
}

func (m IncusMachine) HasSnapshot(ctx context.Context, name string) (bool, error) {
	return m.client.HasSnapshot(ctx, m.ref, name)
}

func (m IncusMachine) Usage(ctx context.Context) (int64, error) {
	return m.client.VolumeUsage(ctx, m.poolName(), m.ref)
}

func info(inst incus.Instance) Info {
	return Info{
		Ref:        inst.Name,
		State:      inst.Status,
		IPv4:       inst.IPv4(),
		LastUsedAt: inst.LastUsedAt,
	}
}

// notFound is err, as this package's ErrNotFound when Incus said the machine
// isn't there, so a caller can ask the question without knowing Incus.
func notFound(err error) error {
	if errors.Is(err, incus.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, err)
	}
	return err
}
