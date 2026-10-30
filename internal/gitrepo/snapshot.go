package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
)

// Snapshot commits never land on a branch, so they don't depend on the user's
// git identity.
var snapshotIdentity = []string{
	"GIT_AUTHOR_NAME=AgentBox", "GIT_AUTHOR_EMAIL=agentbox@localhost",
	"GIT_COMMITTER_NAME=AgentBox", "GIT_COMMITTER_EMAIL=agentbox@localhost",
}

// worktreeTree writes a tree object of the worktree's tracked and untracked
// files (ignored files excluded, unless its index tracks them). It stages into
// a temporary index, so the worktree's own index is untouched.
//
// The temporary index starts as a copy of the worktree's own: its file stats
// let `add --all` hash only what changed. Started from HEAD instead, it hashes
// every file, which over the Mac's shared folder took 49s for 14k files. A
// copy git can't use (torn mid-write, say) falls back to that.
func worktreeTree(worktree string) (string, error) {
	tmp, err := os.MkdirTemp("", "agentbox-index-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	index := filepath.Join(tmp, "index")
	env := []string{"GIT_INDEX_FILE=" + index}
	stage := func() (string, error) {
		if _, err := output(worktree, env, "add", "--all"); err != nil {
			return "", err
		}
		tree, err := output(worktree, env, "write-tree")
		return strings.TrimSpace(tree), err
	}
	if copyIndex(worktree, index) == nil {
		if tree, err := stage(); err == nil {
			return tree, nil
		}
		_ = os.Remove(index)
	}
	if _, err := output(worktree, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	return stage()
}

// copyIndex copies the worktree's index file to dst, mtime included: git
// rehashes a file changed in the same second as the index was written only
// while the index keeps that time, so a fresh one would trust the stale stat.
func copyIndex(worktree, dst string) error {
	src, err := run(worktree, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// SnapshotWorktree records the worktree's files as a commit whose parent is
// HEAD, and points ref at it.
func SnapshotWorktree(worktree, ref, message string) (string, error) {
	tree, err := worktreeTree(worktree)
	if err != nil {
		return "", err
	}
	head, err := run(worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	commit, err := output(worktree, snapshotIdentity, "commit-tree", tree, "-p", head, "-m", message)
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	if _, err := run(worktree, "update-ref", ref, commit); err != nil {
		return "", err
	}
	return commit, nil
}

// RestoreWorktree puts a worktree back to a SnapshotWorktree commit: the
// branch returns to the commit that was HEAD then, and the files match the
// snapshot. Ignored files, like node_modules or .env, are left alone.
func RestoreWorktree(worktree, snapshot string) error {
	head, err := run(worktree, "rev-parse", snapshot+"^")
	if err != nil {
		return err
	}
	if _, err := run(worktree, "reset", "--quiet", "--hard", head); err != nil {
		return err
	}
	if _, err := run(worktree, "clean", "--force", "-d", "--quiet"); err != nil {
		return err
	}
	return ApplyTree(worktree, snapshot)
}

// ApplyTree makes a clean worktree's files match commit's tree without moving
// HEAD or staging anything, so the differences show up as uncommitted changes.
func ApplyTree(worktree, commit string) error {
	if _, err := run(worktree, "read-tree", "-m", "-u", "HEAD", commit); err != nil {
		return err
	}
	_, err := run(worktree, "reset", "--quiet")
	return err
}

func (r Repo) ResolveRef(ref string) (string, error) {
	return run(r.Root, "rev-parse", "--verify", "--quiet", ref)
}

func (r Repo) DeleteRef(ref string) error {
	_, err := run(r.Root, "update-ref", "-d", ref)
	return err
}

// Refs lists the full names of refs under prefix, like refs/agentbox/snapshots/agent-01/.
func (r Repo) Refs(prefix string) ([]string, error) {
	out, err := run(r.Root, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
