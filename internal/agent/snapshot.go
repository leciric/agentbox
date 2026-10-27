package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"agentbox/internal/gitrepo"
	"agentbox/internal/machine"
	"agentbox/internal/state"
	"agentbox/internal/workspace"
)

// A snapshot has two halves: the machine (an Incus snapshot) and the worktree
// (a commit on refs/agentbox/snapshots/<agent>/<name> holding tracked and
// untracked files, whose parent is the branch's commit at that moment). The
// worktree is a host mount, so an Incus snapshot alone would miss it.

// initialSnapshot is taken when an agent is created, so it can always go back.
const initialSnapshot = "initial"

var snapshotNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

func snapshotRef(agent, name string) string { return "refs/agentbox/snapshots/" + agent + "/" + name }

type Snapshot struct {
	Name      string
	CreatedAt time.Time
	Head      string // the commit the agent's branch was on
}

// Snapshot captures the agent's machine and worktree. With consistent, the
// agent is paused while both halves are taken, so they match exactly.
func (m *Manager) Snapshot(ctx context.Context, a state.Agent, name string, consistent bool) (Snapshot, error) {
	if name == "" {
		name = "snap-" + time.Now().Format("20060102-150405")
	}
	if !snapshotNameRe.MatchString(name) || name == "rm" || strings.HasPrefix(name, "base-") || strings.HasPrefix(name, "fork-") || strings.HasPrefix(name, "turn-") {
		return Snapshot{}, fmt.Errorf("invalid snapshot name %q: use lowercase letters, digits, dots, dashes and underscores (rm, base-*, fork-* and turn-* are reserved)", name)
	}
	return m.takeSnapshot(ctx, a, name, consistent)
}

func (m *Manager) takeSnapshot(ctx context.Context, a state.Agent, name string, consistent bool) (Snapshot, error) {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return Snapshot{}, err
	}
	ref := snapshotRef(a.Name, name)
	if _, err := repo.ResolveRef(ref); err == nil {
		return Snapshot{}, fmt.Errorf("%s already has a snapshot named %q", a.Ref(), name)
	}
	mach := m.machine(a)
	inst, err := mach.Info(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	running := inst.State == "Running"

	// A pod's worktree is on its claim, so its git half needs the pod. Starting
	// a stopped one for the snapshot, and stopping it again afterwards, is what
	// makes snapshotting a stopped cluster agent work.
	if a.OnCluster() && !running {
		if err := mach.Start(ctx); err != nil {
			return Snapshot{}, err
		}
		if _, err := mach.WaitReady(ctx, podReadyTimeout); err != nil {
			_ = mach.Stop(context.WithoutCancel(ctx))
			return Snapshot{}, err
		}
		running = true
		defer func() { _ = mach.Stop(context.WithoutCancel(ctx)) }()
	}

	message := "agentbox snapshot " + a.Ref() + "@" + name
	ws := m.workspace(a)
	commit, err := ws.Snapshot(ctx, ref, message)
	if err != nil && running && strings.Contains(err.Error(), "ermission denied") {
		m.handBackFiles(ctx, a)
		commit, err = ws.Snapshot(ctx, ref, message)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshotting the worktree: %w", err)
	}
	if consistent && running {
		// Freeze the machine, then record the worktree again, so both halves
		// match exactly. A pod cannot be frozen: its archive stops it, which is
		// the same consistency, so the second worktree read is skipped.
		err := mach.Pause(ctx)
		switch {
		case errors.Is(err, machine.ErrNotSupported):
		case err != nil:
			_ = repo.DeleteRef(ref)
			return Snapshot{}, err
		default:
			defer func() { _ = mach.Resume(context.WithoutCancel(ctx)) }()
			if commit, err = ws.Snapshot(ctx, ref, message); err != nil {
				_ = repo.DeleteRef(ref)
				return Snapshot{}, fmt.Errorf("snapshotting the worktree: %w", err)
			}
		}
	}
	head, err := m.snapshotHead(ctx, a, ws, repo, commit)
	if err != nil {
		_ = repo.DeleteRef(ref)
		return Snapshot{}, err
	}
	if err := mach.Snapshot(ctx, name); err != nil {
		_ = repo.DeleteRef(ref)
		return Snapshot{}, err
	}
	// A pod's git half is written beside its archive, so a stopped pod's
	// snapshots stay readable; an Incus machine leaves it in the repository.
	if gs, ok := mach.(machine.GitSnapshotter); ok {
		if err := gs.SnapshotGit(ctx, name, ref, commit, head); err != nil {
			_ = mach.DeleteSnapshot(context.WithoutCancel(ctx), name)
			_ = repo.DeleteRef(ref)
			return Snapshot{}, fmt.Errorf("recording the worktree of %s@%s: %w", a.Ref(), name, err)
		}
	}
	return Snapshot{Name: name, CreatedAt: time.Now(), Head: head}, nil
}

// snapshotHead is the commit a snapshot was taken on: the parent of the
// snapshot's own commit. The workspace knows it on both runtimes, because the
// object is in the git repository the worktree belongs to.
func (m *Manager) snapshotHead(ctx context.Context, a state.Agent, ws workspace.Workspace, repo gitrepo.Repo, commit string) (string, error) {
	head, err := ws.ResolveCommit(ctx, commit+"^")
	if err == nil {
		return head, nil
	}
	if head, repoErr := repo.ResolveCommit(commit + "^"); repoErr == nil {
		return head, nil
	}
	return "", fmt.Errorf("reading the commit %s was taken on: %w", commit, err)
}

// Snapshots lists the agent's snapshots, oldest first.
func (m *Manager) Snapshots(ctx context.Context, a state.Agent) ([]Snapshot, error) {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return nil, err
	}
	listed, err := m.machine(a).SnapshotList(ctx)
	if err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	for _, s := range listed {
		if s.Head != "" {
			// A pod records the worktree half with the archive, so its
			// snapshots read the same whether the pod is up or not.
			snapshots = append(snapshots, Snapshot{Name: s.Name, CreatedAt: s.CreatedAt, Head: s.Head})
			continue
		}
		commit, err := repo.ResolveRef(snapshotRef(a.Name, s.Name))
		if err != nil {
			continue // not a complete AgentBox snapshot, like a temporary one from base save
		}
		head, _ := repo.ResolveCommit(commit + "^")
		snapshots = append(snapshots, Snapshot{Name: s.Name, CreatedAt: s.CreatedAt, Head: head})
	}
	return snapshots, nil
}

// Restore puts the agent back to a snapshot: machine, branch and files. The
// state it had before is kept on refs/agentbox/pre-restore/<agent>/<time>, so
// commits made after the snapshot aren't lost.
func (m *Manager) Restore(ctx context.Context, a state.Agent, name string) error {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return err
	}
	mach := m.machine(a)
	commit, err := m.snapshotCommit(ctx, a, repo, name)
	if err != nil {
		return fmt.Errorf("%s has no snapshot %q", a.Ref(), name)
	}
	inst, err := mach.Info(ctx)
	if err != nil {
		return err
	}
	if inst.State == "Frozen" {
		if err := mach.Resume(ctx); err != nil {
			return err
		}
		inst.State = "Running"
	}
	// A pod's worktree is on its claim, so the pre-restore ref can only be
	// taken while the pod is up. A stopped one is started for that, and the
	// restore stops it again for its own archive work.
	if a.OnCluster() && inst.State != "Running" {
		if err := mach.Start(ctx); err != nil {
			return err
		}
		if _, err := mach.WaitReady(ctx, podReadyTimeout); err != nil {
			_ = mach.Stop(context.WithoutCancel(ctx))
			return err
		}
		inst.State = "Running"
	}
	if inst.State == "Running" {
		m.handBackFiles(ctx, a)
	}

	backup := "refs/agentbox/pre-restore/" + a.Name + "/" + time.Now().UTC().Format("20060102-150405")
	if _, err := m.workspace(a).Snapshot(ctx, backup, "before restoring "+name); err != nil {
		return fmt.Errorf("saving the current worktree: %w", err)
	}
	m.logf("Restoring the machine to %s (the current worktree is saved on %s)", name, backup)
	if err := mach.RestoreSnapshot(ctx, name); err != nil {
		return err
	}
	m.logf("Restoring the worktree and branch")
	if err := m.workspace(a).Restore(ctx, commit); err != nil {
		return err
	}
	if inst, err = mach.Info(ctx); err != nil {
		return err
	}
	if inst.State != "Running" && !a.OnCluster() {
		// A snapshot taken by an earlier release brings back the limits it
		// set on the machine.
		if err := m.dropOldLimits(ctx, a.Instance); err != nil {
			return err
		}
		if err := m.settleGPU(ctx, a.Instance); err != nil {
			return err
		}
		// And the CPU share it had then.
		if err := m.setStartShare(ctx, a.Instance); err != nil {
			return err
		}
		m.nodeModulesSources(ctx, a)
		if err := mach.Start(ctx); err != nil {
			return err
		}
	}
	if _, err := mach.WaitReady(ctx, readyTimeout); err != nil {
		return err
	}
	// A snapshot taken before /t was capped brings back its uncapped fstab.
	// A pod has no /t of its own; its tmpfs is the pod's own volume.
	if !a.OnCluster() {
		m.CapTmp(ctx, a)
	}
	// The snapshot holds the secrets file as it was when the snapshot was
	// taken; the agent's secrets are whatever they are now.
	if err := m.WriteSecrets(ctx, a); err != nil {
		return err
	}
	// Its skills too.
	if err := m.WriteSkills(ctx, a); err != nil {
		m.logf("installing the skills of %s: %v", a.Ref(), err)
	}
	return m.ensureSession(ctx, a)
}

func (m *Manager) DeleteSnapshot(ctx context.Context, a state.Agent, name string) error {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return err
	}
	ref := snapshotRef(a.Name, name)
	if _, err := m.snapshotCommit(ctx, a, repo, name); err != nil {
		return fmt.Errorf("%s has no snapshot %q", a.Ref(), name)
	}
	if err := m.machine(a).DeleteSnapshot(ctx, name); err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	return repo.DeleteRef(ref)
}

// snapshotCommit is the commit a snapshot's worktree is on: the snapshot ref
// its record names. A pod keeps it beside the archive, and an Incus machine's
// lives in the project's repository.
func (m *Manager) snapshotCommit(ctx context.Context, a state.Agent, repo gitrepo.Repo, name string) (string, error) {
	if gs, ok := m.machine(a).(machine.GitSnapshotter); ok {
		commit, _, err := gs.SnapshotGitInfo(ctx, name)
		return commit, err
	}
	return repo.ResolveRef(snapshotRef(a.Name, name))
}

type ForkOptions struct {
	Name     string // default: the next free agent-NN
	Title    string // default: the source's title, marked as a fork
	Snapshot string // default: a snapshot taken now and deleted afterwards
	// Checkpoint, instead of a snapshot, starts the fork's branch and files
	// where one of the source's checkpoints has them, on a copy of its machine
	// as it is now.
	Checkpoint string
}

// Fork creates a new agent from a snapshot of another one: a copy of its
// machine, and a new branch starting where the snapshot's branch was, with the
// snapshot's uncommitted and untracked files still uncommitted.
func (m *Manager) Fork(ctx context.Context, src state.Agent, opts ForkOptions) (state.Agent, error) {
	if err := validateName(opts.Name); err != nil {
		return state.Agent{}, err
	}
	title, err := CleanTitle(opts.Title)
	if err != nil {
		return state.Agent{}, err
	}
	if title == "" && src.Title != "" {
		title, _ = CleanTitle(src.Title + " (fork)")
	}
	p, repo, err := m.project(ctx, src.Project)
	if err != nil {
		return state.Agent{}, err
	}
	// The fork keeps the original's accounts.
	logins, err := m.CheckLogin(src.AI, p, Logins{Claude: src.ClaudeAccount, Codex: src.CodexAccount, OpenCode: src.OpenCodeAccounts, Cursor: src.CursorAccount})
	if err != nil {
		return state.Agent{}, err
	}
	if opts.Snapshot != "" && opts.Checkpoint != "" {
		return state.Agent{}, errors.New("fork from a snapshot or from a checkpoint, not both")
	}
	if src.OnCluster() && opts.Checkpoint != "" {
		return state.Agent{}, fmt.Errorf("a fork of %s starts from a snapshot, not a checkpoint: its machine is an archive of its claim, and a checkpoint has only the worktree's half", src.Ref())
	}
	var cp state.Checkpoint
	if opts.Checkpoint != "" {
		if cp, err = m.Store.Checkpoint(ctx, src.Project, src.Name, CheckpointID(opts.Checkpoint)); err != nil {
			return state.Agent{}, err
		}
		if _, err := repo.ResolveRef(cp.Ref); err != nil {
			return state.Agent{}, fmt.Errorf("%s's checkpoint %s is gone from the repository", src.Ref(), cp.ID)
		}
	}
	name := opts.Snapshot
	if name == "" {
		name = "fork-" + time.Now().UTC().Format("20060102-150405")
		m.logf("Snapshotting %s", src.Ref())
		if _, err := m.takeSnapshot(ctx, src, name, false); err != nil {
			return state.Agent{}, err
		}
		defer func() { _ = m.DeleteSnapshot(context.WithoutCancel(ctx), src, name) }()
	}
	tree, err := m.snapshotCommit(ctx, src, repo, name)
	if err != nil {
		return state.Agent{}, fmt.Errorf("%s has no snapshot %q", src.Ref(), name)
	}
	head, err := m.snapshotHead(ctx, src, m.workspace(src), repo, tree)
	if err != nil {
		return state.Agent{}, err
	}
	baseRef := src.Ref() + "@" + name
	switch {
	case cp.ID != "":
		tree, head, baseRef = cp.Commit, cp.Head, src.Ref()+"@"+cp.ID
	case opts.Snapshot == "":
		baseRef = src.Ref()
	}
	pl := plan{
		project:    p,
		repo:       repo,
		name:       opts.Name,
		title:      title,
		ai:         src.AI,
		autonomous: src.Autonomous,
		logins:     logins,
		iface:      src.Interface,
		baseRef:    baseRef,
		baseCommit: head,
		tree:       tree,
		copyEnv:    true,
		connectors: src.Connectors, // a copy is given what its source was
	}
	if src.OnCluster() {
		// A cluster fork stays on its source's cluster: its claim archive can
		// only be read back into a claim there, and the new pod needs the same
		// gateway and git profile to carry on.
		pl.cluster = src.Cluster
		pl.claimFrom = src.Ref() + "@" + name
	} else {
		pl.source = src.Instance + "/" + name
	}
	return m.build(ctx, pl)
}

func deleteAgentRefs(repo gitrepo.Repo, agent string) {
	for _, prefix := range []string{"refs/agentbox/snapshots/" + agent + "/", "refs/agentbox/pre-restore/" + agent + "/"} {
		refs, _ := repo.Refs(prefix)
		for _, ref := range refs {
			_ = repo.DeleteRef(ref)
		}
	}
}
