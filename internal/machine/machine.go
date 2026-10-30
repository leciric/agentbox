// Package machine is the seam between the agent manager and the runtime an
// agent's machine runs on: an Incus instance on this host, or a pod in a
// Kubernetes cluster.
//
// The manager asks a Machine for the operations that mean the same thing
// everywhere — start, stop, exec, files, snapshots — and keeps what only one
// runtime has, like Incus devices or pod scheduling, behind the Runtime it
// names. So that the Incus path keeps behaving exactly as it did, this
// package's Incus implementation only forwards to internal/incus.
package machine

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"time"
)

// ErrNotFound is the error a Runtime returns for a machine, or a snapshot,
// that isn't there.
var ErrNotFound = errors.New("machine not found")

// ErrNotSupported is the error for a machine operation its runtime has no
// meaning for: a pod has no freeze, and its disk is a volume, not a snapshot
// the runtime takes.
var ErrNotSupported = errors.New("not supported by this machine's runtime")

// Kind is the runtime an agent's machine runs on.
type Kind string

const (
	KindIncus      Kind = "incus"
	KindKubernetes Kind = "kubernetes"
)

// Info is what a runtime says about one machine.
type Info struct {
	// Ref is the machine's name in its runtime: the Incus instance name, or
	// the pod name.
	Ref string
	// State is the runtime's own word for what it is doing: Running, Stopped
	// or Frozen on Incus.
	State string
	// IPv4 is the machine's address on the runtime's network, "" when it has
	// none.
	IPv4 string
	// LastUsedAt is when the machine last started, as the runtime recorded
	// it. Zero when the runtime doesn't say.
	LastUsedAt time.Time
}

// Running reports whether the machine is up.
func (i Info) Running() bool { return i.State == "Running" }

// Paused reports whether the machine is up but frozen, as Incus pauses one.
func (i Info) Paused() bool { return i.State == "Frozen" }

// Snapshot is a machine snapshot as its runtime lists it.
type Snapshot struct {
	Name      string
	CreatedAt time.Time
	// Head is the commit the agent's branch was on when the snapshot was
	// taken, when the runtime records the git half itself. A pod does, because
	// its worktree is on the claim rather than in a host repository; an Incus
	// machine leaves it empty and the manager resolves its ref in the project.
	Head string
}

// AttachOptions is Attach's.
type AttachOptions struct {
	// Argv is what runs in the terminal, in the machine's own environment:
	// the agent's shell and the tmux session its tools live in.
	Argv []string
	// Cols and Rows are the terminal's size as it opens.
	Cols, Rows uint16
}

// Terminal is a machine's terminal: what it prints, and what is typed into it.
type Terminal interface {
	io.ReadWriteCloser
	// Resize changes the terminal's size.
	Resize(cols, rows uint16) error
}

// Machine is one agent's machine in a runtime. Every method names the machine
// the value was made for, so the manager never passes a name around.
type Machine interface {
	Ref() string
	Kind() Kind

	// Info reads the machine's current state.
	Info(ctx context.Context) (Info, error)
	// WaitReady waits for the machine to finish booting, at most timeout.
	WaitReady(ctx context.Context, timeout time.Duration) (Info, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// StopWithin asks the machine to shut down, and forcibly stops it if it
	// hasn't within timeout.
	StopWithin(ctx context.Context, timeout time.Duration) error
	ForceStop(ctx context.Context) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	// Destroy deletes the machine, snapshots and all.
	Destroy(ctx context.Context) error

	// Exec runs a command as root and returns its combined output.
	Exec(ctx context.Context, command ...string) (string, error)
	// ExecInput is Exec with something on the command's stdin.
	ExecInput(ctx context.Context, stdin io.Reader, command ...string) (string, error)
	// UserExec runs command, a shell command line, as user, with the given
	// streams. A nil stream is discarded.
	UserExec(ctx context.Context, user, command string, stdin io.Reader, stdout, stderr io.Writer) error

	// Attach runs opts.Argv in a terminal on the machine, for what an
	// interactive session needs. The terminal ends when the command does or
	// when it is closed.
	Attach(ctx context.Context, opts AttachOptions) (Terminal, error)

	// WriteFile writes content to path, owned by uid and gid, with mode.
	WriteFile(ctx context.Context, path string, content []byte, uid, gid int, mode os.FileMode) error
	// PullFile copies file from the machine to target on this host.
	PullFile(ctx context.Context, file, target string) error
	// PullDir copies dir from the machine to dir on this host.
	PullDir(ctx context.Context, from, dir string) error
	// PushFile copies local on this host to file in the machine.
	PushFile(ctx context.Context, local, file string, mode os.FileMode) error

	// Snapshot records the machine's disk under name.
	Snapshot(ctx context.Context, name string) error
	SnapshotList(ctx context.Context) ([]Snapshot, error)
	RestoreSnapshot(ctx context.Context, name string) error
	DeleteSnapshot(ctx context.Context, name string) error
	HasSnapshot(ctx context.Context, name string) (bool, error)

	// Usage is the bytes the machine's disk holds.
	Usage(ctx context.Context) (int64, error)

	// Dial opens a connection from inside the machine, for the unix socket
	// an Incus proxy device exposes on this host or a port only the pod can
	// reach.
	Dial(ctx context.Context, network, addr string) (net.Conn, error)
}

// Runtime is a place machines run: Incus on this host, or one Kubernetes
// cluster.
type Runtime interface {
	Kind() Kind
	// Machine is the machine a runtime names, valid to use from the moment it
	// is called: no runtime lookup happens until a method is.
	Machine(ref string) Machine
}

// ClaimPreparer is a Machine whose disk can be primed from a snapshot another
// machine took: a pod, whose disk is a volume. An Incus machine has no need of
// it, because its snapshots are copies of a whole instance already.
type ClaimPreparer interface {
	// PrepareClaim unpacks the snapshot from, project/agent@name, into claim,
	// and writes what the agent's row needs to start on it.
	PrepareClaim(ctx context.Context, claim, from string) error
}

// Recreator is a Machine the runtime can replace on its own disk: a pod, whose
// machine is its claim and whose pod is a use of it. Recreating deletes the pod,
// keeps the claim, and starts a new pod at the image the cluster runs now, with
// nothing of the old pod's processes or container filesystem. An Incus machine
// does not implement this: its machine is the instance, and a machine that is
// gone is what Manager.Recreate makes from the base image.
type Recreator interface {
	Recreate(ctx context.Context) error
}

// GitSnapshotter is a Machine that keeps the git half of a snapshot beside the
// snapshot itself: a pod has to, because its worktree lives on the claim and
// this host has no ref for it, and that is also what lets a stopped pod's
// snapshots be listed and restored. An Incus machine leaves the git half in the
// project's own repository, so it does not implement this.
type GitSnapshotter interface {
	// SnapshotGit records the ref, the commit whose tree is the snapshot and
	// the commit it was taken on, in the snapshot named name.
	SnapshotGit(ctx context.Context, name, ref, commit, head string) error
	// SnapshotGitInfo is what that snapshot recorded. Both are "" when the
	// snapshot has no record, which is a snapshot that failed partway.
	SnapshotGitInfo(ctx context.Context, name string) (commit, head string, err error)
}
