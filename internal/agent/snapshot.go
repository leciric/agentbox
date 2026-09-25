package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
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
	if !snapshotNameRe.MatchString(name) || name == "rm" || strings.HasPrefix(name, "base-") || strings.HasPrefix(name, "fork-") {
		return Snapshot{}, fmt.Errorf("invalid snapshot name %q: use lowercase letters, digits, dots, dashes and underscores (rm, base-* and fork-* are reserved)", name)
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
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return Snapshot{}, err
	}
	running := inst.Status == "Running"

	message := "agentbox snapshot " + a.Ref() + "@" + name
	commit, err := gitrepo.SnapshotWorktree(a.Worktree, ref, message)
	if err != nil && running && strings.Contains(err.Error(), "ermission denied") {
		m.handBackFiles(ctx, a)
		commit, err = gitrepo.SnapshotWorktree(a.Worktree, ref, message)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshotting the worktree: %w", err)
	}
	if consistent && running {
		// Freeze the machine, then record the worktree again, so both halves match exactly.
		if _, err := m.Incus.Run(ctx, "pause", a.Instance); err != nil {
			repo.DeleteRef(ref)
			return Snapshot{}, err
		}
		defer m.Incus.Run(context.WithoutCancel(ctx), "resume", a.Instance)
		if commit, err = gitrepo.SnapshotWorktree(a.Worktree, ref, message); err != nil {
			repo.DeleteRef(ref)
			return Snapshot{}, fmt.Errorf("snapshotting the worktree: %w", err)
		}
	}
	if _, err := m.Incus.Run(ctx, "snapshot", "create", a.Instance, name); err != nil {
		repo.DeleteRef(ref)
		return Snapshot{}, err
	}
	head, _ := repo.ResolveCommit(commit + "^")
	return Snapshot{Name: name, CreatedAt: time.Now(), Head: head}, nil
}

// Snapshots lists the agent's snapshots, oldest first.
func (m *Manager) Snapshots(ctx context.Context, a state.Agent) ([]Snapshot, error) {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return nil, err
	}
	machine, err := m.Incus.Snapshots(ctx, a.Instance)
	if err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	for _, s := range machine {
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
	commit, err := repo.ResolveRef(snapshotRef(a.Name, name))
	if err != nil {
		return fmt.Errorf("%s has no snapshot %q", a.Ref(), name)
	}
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return err
	}
	if inst.Status == "Frozen" {
		if _, err := m.Incus.Run(ctx, "resume", a.Instance); err != nil {
			return err
		}
		inst.Status = "Running"
	}
	if inst.Status == "Running" {
		m.handBackFiles(ctx, a)
	}

	backup := "refs/agentbox/pre-restore/" + a.Name + "/" + time.Now().UTC().Format("20060102-150405")
	if _, err := gitrepo.SnapshotWorktree(a.Worktree, backup, "before restoring "+name); err != nil {
		return fmt.Errorf("saving the current worktree: %w", err)
	}
	m.logf("Restoring the machine to %s (the current worktree is saved on %s)", name, backup)
	if _, err := m.Incus.Run(ctx, "snapshot", "restore", a.Instance, name); err != nil {
		return err
	}
	m.logf("Restoring the worktree and branch")
	if err := gitrepo.RestoreWorktree(a.Worktree, commit); err != nil {
		return err
	}
	if inst, err = m.Incus.Instance(ctx, a.Instance); err != nil {
		return err
	}
	if inst.Status != "Running" {
		if _, err := m.Incus.Run(ctx, "start", a.Instance); err != nil {
			return err
		}
	}
	if _, err := m.Incus.WaitReady(ctx, a.Instance, readyTimeout); err != nil {
		return err
	}
	// The snapshot holds the secrets file as it was when the snapshot was
	// taken; the agent's secrets are whatever they are now.
	if err := m.WriteSecrets(ctx, a); err != nil {
		return err
	}
	return m.ensureSession(ctx, a)
}

func (m *Manager) DeleteSnapshot(ctx context.Context, a state.Agent, name string) error {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return err
	}
	ref := snapshotRef(a.Name, name)
	if _, err := repo.ResolveRef(ref); err != nil {
		return fmt.Errorf("%s has no snapshot %q", a.Ref(), name)
	}
	if _, err := m.Incus.Run(ctx, "snapshot", "delete", a.Instance, name); err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	return repo.DeleteRef(ref)
}

type ForkOptions struct {
	Name     string // default: the next free agent-NN
	Title    string // default: the source's title, marked as a fork
	Snapshot string // default: a snapshot taken now and deleted afterwards
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
	// The fork keeps the original's Claude Code account.
	account, err := m.CheckLogin(src.AI, p, src.ClaudeAccount)
	if err != nil {
		return state.Agent{}, err
	}
	name := opts.Snapshot
	if name == "" {
		name = "fork-" + time.Now().UTC().Format("20060102-150405")
		m.logf("Snapshotting %s", src.Ref())
		if _, err := m.takeSnapshot(ctx, src, name, false); err != nil {
			return state.Agent{}, err
		}
		defer m.DeleteSnapshot(context.WithoutCancel(ctx), src, name)
	}
	tree, err := repo.ResolveRef(snapshotRef(src.Name, name))
	if err != nil {
		return state.Agent{}, fmt.Errorf("%s has no snapshot %q", src.Ref(), name)
	}
	head, err := repo.ResolveCommit(tree + "^")
	if err != nil {
		return state.Agent{}, err
	}
	baseRef := src.Ref() + "@" + name
	if opts.Snapshot == "" {
		baseRef = src.Ref()
	}
	// A fork is the same work on the same machine, so it is capped like the
	// agent it came from rather than like a new agent: an agent someone had
	// given more cores keeps them, and one that was narrowed stays narrowed.
	// `incus copy` would carry the keys over anyway; build sets them itself, so
	// what a fork is capped at is decided here rather than inherited by accident.
	limits, err := m.Limits(ctx, src)
	if err != nil {
		return state.Agent{}, err
	}
	return m.build(ctx, plan{
		project:       p,
		repo:          repo,
		name:          opts.Name,
		title:         title,
		ai:            src.AI,
		autonomous:    src.Autonomous,
		claudeAccount: account,
		iface:         src.Interface,
		source:        src.Instance + "/" + name,
		baseRef:       baseRef,
		baseCommit:    head,
		tree:          tree,
		copyEnv:       true,
		limits:        limits,
	})
}

func deleteAgentRefs(repo gitrepo.Repo, agent string) {
	for _, prefix := range []string{"refs/agentbox/snapshots/" + agent + "/", "refs/agentbox/pre-restore/" + agent + "/"} {
		refs, _ := repo.Refs(prefix)
		for _, ref := range refs {
			repo.DeleteRef(ref)
		}
	}
}
