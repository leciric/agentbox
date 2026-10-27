package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"agentbox/internal/gitrepo"
)

// Local is a Workspace on this host: a linked worktree of the project's
// repository.
type Local struct {
	// Dir is the worktree on this host.
	Dir string
}

// Path is the worktree's directory.
func (w Local) Path() string { return w.Dir }

// Prepare makes the worktree on a new branch. A failure removes what it made,
// so the manager's own undo list has nothing to answer for.
func (w Local) Prepare(ctx context.Context, repo gitrepo.Repo, opts PrepareOptions) error {
	if err := repo.AddWorktree(w.Dir, opts.Branch, opts.Base, opts.Reason); err != nil {
		return err
	}
	fail := func(step string, err error) error {
		_ = repo.RemoveWorktree(w.Dir)
		// AddWorktree made the branch, so a half-made workspace leaves no
		// branch behind for the next create to trip over.
		_ = repo.DeleteBranch(opts.Branch)
		return &StepError{Step: step, Err: err}
	}
	if opts.Tree != "" {
		if err := gitrepo.ApplyTree(w.Dir, opts.Tree); err != nil {
			return fail("worktree", err)
		}
	}
	if len(opts.Env) > 0 {
		if err := copyEnv(opts.EnvFrom, w.Dir, opts.Env); err != nil {
			return fail("env files", err)
		}
	}
	return nil
}

// Diff is what the worktree changed against its base: the project's base
// branch as it is now, so work taken in by a merge or a rebase is not the
// agent's.
func (w Local) Diff(ctx context.Context, opts DiffOptions) (string, error) {
	base := gitrepo.DiffBase(w.Dir, opts.BaseRef, opts.BaseCommit, opts.Onto...)
	return gitrepo.DiffWorktree(w.Dir, base, opts.Stat, opts.Paths...)
}

// Files lists the worktree's tracked and untracked files.
func (w Local) Files(ctx context.Context, limit int) ([]string, bool, error) {
	return gitrepo.ListFiles(w.Dir, limit)
}

// ReadFile reads a file of the worktree.
func (w Local) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if name == "" || !fs.ValidPath(name) {
		return nil, fmt.Errorf("%q is not a path inside the workspace", name)
	}
	return os.ReadFile(filepath.Join(w.Dir, filepath.FromSlash(name)))
}

// Checkpoint commits the worktree's files to ref, leaving its branch, index
// and files as they were.
func (w Local) Checkpoint(ctx context.Context, ref, message string) (string, error) {
	return gitrepo.SnapshotWorktree(w.Dir, ref, message)
}

// Snapshot is Checkpoint on this host: the machine's own disk is what the
// Machine snapshots, and the worktree is all the workspace holds.
func (w Local) Snapshot(ctx context.Context, ref, message string) (string, error) {
	return gitrepo.SnapshotWorktree(w.Dir, ref, message)
}

// Restore puts the branch back where commit's tree stood and the files back as
// they were, leaving ignored files alone.
func (w Local) Restore(ctx context.Context, commit string) error {
	return gitrepo.RestoreWorktree(w.Dir, commit)
}

// Status reads whether the worktree is on disk and whether git sees changes in
// it. A worktree that isn't there is not an error: there is nothing dirty in
// one that doesn't exist.
func (w Local) Status(ctx context.Context) (Status, error) {
	st := Status{Exists: worktreeExists(w.Dir)}
	if !st.Exists {
		return st, nil
	}
	dirty, err := gitrepo.Dirty(w.Dir)
	st.Dirty = dirty
	return st, err
}

// Destroy removes the worktree, discarding what is uncommitted in it.
func (w Local) Destroy(ctx context.Context, repo gitrepo.Repo) error {
	return repo.RemoveWorktree(w.Dir)
}

// worktreeExists reports whether dir may still hold a worktree: anything but a
// path that is definitely not there, as os.Stat tells it.
func worktreeExists(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(dir)
	return !errors.Is(err, fs.ErrNotExist)
}

// copyEnv copies the project's env files into a new tree, keeping each file's
// mode: they are the ones an agent is expected to find and use, not commit.
func copyEnv(srcRoot, dstRoot string, rel []string) error {
	for _, r := range rel {
		src, dst := filepath.Join(srcRoot, r), filepath.Join(dstRoot, r)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, content, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}
