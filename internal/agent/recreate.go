package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// RecreateOptions are Recreate's.
type RecreateOptions struct {
	// Limits caps the new machine, the way Create's does: a field nobody set
	// falls back to the installation's default. A choice this machine can't
	// take (more memory than it has) falls back to the default too, and says
	// so, rather than leaving the agent without a machine.
	Limits LimitChoice
	// Home is a directory whose contents go into the agent user's home in the
	// new machine before its AI tool starts: what the old machine's chat
	// sessions were, so its chat resumes where it was. Optional.
	Home string
	// Stopped leaves the new machine stopped once it's made, for an agent that
	// wasn't running.
	Stopped bool
}

// ErrHasMachine is Recreate on an agent whose machine is there already.
var ErrHasMachine = errors.New("it has its machine already")

// Recreate gives an agent whose machine is gone a new one, from the base
// image: what `agentbox vm migrate` does for every agent it moves into the
// VM, and what an agent whose Incus instance was deleted by hand can use to
// carry on. Everything outside the machine stays as it is and is what the new
// one is made for: the agent's record (title, model, accounts, chat), its
// worktree with whatever is uncommitted in it, and its branch. What was only
// inside the old machine — packages installed in it, its home directory
// outside the worktree — isn't there any more.
//
// An agent that has a machine is left alone (ErrHasMachine), unless an
// interrupted Recreate made it: the agent is marked creating while Recreate
// runs, so that machine is replaced. A failure removes the half-made machine
// and leaves the agent as it was, to try again.
func (m *Manager) Recreate(ctx context.Context, a state.Agent, opts RecreateOptions) error {
	if a.IsLead() {
		return fmt.Errorf("%s is its project's chat, which has no machine", a.Ref())
	}
	p, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return err
	}
	if !repo.HasWorktree(a.Worktree) {
		return fmt.Errorf("%s's worktree %s isn't there, so there's nothing to make its machine for", a.Ref(), a.Worktree)
	}
	switch _, err := m.Incus.Instance(ctx, a.Instance); {
	case err == nil && a.Status == state.AgentReady:
		return fmt.Errorf("%s: %w (%s)", a.Ref(), ErrHasMachine, a.Instance)
	case err == nil:
		m.logf("Removing %s, which an interrupted run left half-made", a.Instance)
		if err := m.Incus.Delete(ctx, a.Instance); err != nil {
			return err
		}
	case !errors.Is(err, incus.ErrNotFound):
		return err
	}
	ready, err := image.Ready(ctx, m.Incus)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("the base image isn't built: run agentbox image build")
	}
	defaults, err := m.Defaults(ctx)
	if err != nil {
		return err
	}
	limits, err := opts.Limits.Resolve(defaults)
	if err == nil {
		err = fitsHere(limits, HostMemory())
	}
	if err != nil {
		m.logf("Its limits don't fit here (%v): it gets what new agents get, %s", err, defaults.Describe())
		limits = defaults
	}

	if err := m.Store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentCreating); err != nil {
		return err
	}
	cleanup := context.WithoutCancel(ctx)
	var undo []func()
	fail := func(step string, err error) error {
		m.logf("%s failed; removing the new machine", step)
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		_ = m.Store.SetAgentStatus(cleanup, a.Project, a.Name, state.AgentReady)
		return fmt.Errorf("making %s's machine: %s: %w", a.Ref(), step, err)
	}
	if step, err := m.makeMachine(ctx, a, repo, image.SnapshotRef(), limits, nil, "", opts.Home, &undo); err != nil {
		return fail(step, err)
	}
	// The first snapshot's machine half was a machine fresh from the base,
	// which is what this one is: it stays restorable. Later snapshots' were
	// the old machine, and went with it; their worktree halves stay on their
	// refs.
	if _, err := repo.ResolveRef(snapshotRef(a.Name, initialSnapshot)); err == nil {
		if err := m.Incus.CreateSnapshot(ctx, a.Instance, initialSnapshot); err != nil {
			return fail("snapshot", err)
		}
	}
	if err := m.Store.SetPausedAt(ctx, a.Project, a.Name, time.Time{}); err != nil {
		return fail("state", err)
	}
	if err := m.Store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentReady); err != nil {
		return fail("state", err)
	}
	a.Status = state.AgentReady
	m.EnsureBrowser(ctx, a)
	m.EnsureNesting(ctx, a, p)
	if opts.Stopped {
		m.logf("Stopping it, as it was")
		return m.Stop(ctx, a)
	}
	return nil
}

// fitsHere says why limits can't be a machine's on a host with memory bytes of
// memory (the VM's cap, in AgentBox's VM): a memory ceiling above it, which a
// machine moved from a bigger host can have.
func fitsHere(limits Limits, memory int64) error {
	if limits.Memory == "" || memory <= 0 {
		return nil
	}
	limit, err := ParseBytes(limits.Memory)
	if err != nil || limit <= memory {
		return nil // a percentage of the host fits by definition
	}
	return fmt.Errorf("a memory limit of %s is more than the %s this machine has", limits.Memory, HumanBytes(memory))
}

// pushHome copies the contents of dir into the agent user's home, as that
// user, keeping what the home has that dir doesn't.
func (m *Manager) pushHome(ctx context.Context, a state.Agent, dir string) error {
	var archive bytes.Buffer
	if err := tarDir(&archive, dir); err != nil {
		return err
	}
	var out bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, "tar -x -C \"$HOME\" -f -", &archive, &out, &out); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// tarDir writes dir's files, directories and symlinks to w as a tar archive,
// with paths relative to dir.
func tarDir(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			return nil // sockets and the like mean nothing in another machine
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
