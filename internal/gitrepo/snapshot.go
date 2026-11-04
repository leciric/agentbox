package gitrepo

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	if tree, ok := persistentTree(worktree); ok {
		return tree, nil
	}
	return copiedTree(worktree)
}

// copiedTree is worktreeTree through a throwaway copy of the worktree's index.
func copiedTree(worktree string) (string, error) {
	tmp, err := os.MkdirTemp("", "agentbox-index-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	index := filepath.Join(tmp, "index")
	if copyIndex(worktree, index) == nil {
		if tree, err := stageTree(worktree, index); err == nil {
			return tree, nil
		}
		_ = os.Remove(index)
	}
	if _, err := output(worktree, []string{"GIT_INDEX_FILE=" + index}, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	return stageTree(worktree, index)
}

// stageTree stages the worktree into the index file and writes its tree.
func stageTree(worktree, index string) (string, error) {
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := output(worktree, env, "add", "--all"); err != nil {
		return "", err
	}
	tree, err := output(worktree, env, "write-tree")
	return strings.TrimSpace(tree), err
}

// The persistent index: copying the worktree's index for every measurement
// leaves the copy with the stat information the worktree's own index last
// had, which over virtiofs made `add --all` ~350ms where a warm index took
// ~100ms. With a directory set (SetIndexDir, the daemon's state), each
// worktree keeps one temporary index there between calls, so the stats
// `add --all` refreshes survive. It is never the worktree's own index, and
// `add --all` makes the tree match the files whatever it started from; only
// what the worktree's index tracks despite .gitignore could go stale, so it
// is seeded again from a copy when the worktree's index changes size (an
// entry added or removed; a stat refresh keeps it) and every
// persistentIndexMaxAge. One that git can't use is dropped for the copy.
var (
	indexDir   string
	indexMu    sync.Mutex
	indexState = map[string]*persistentIndex{}
)

const (
	persistentIndexMaxAge  = 10 * time.Minute
	persistentIndexMaxIdle = 14 * 24 * time.Hour
)

type persistentIndex struct {
	mu       sync.Mutex // held while git uses the file
	seededAt time.Time
	srcSize  int64 // the worktree's index size when seeded
}

// SetIndexDir turns the persistent index on, keeping the files in dir, and
// forgets those not used for two weeks (removed worktrees').
func SetIndexDir(dir string) {
	indexMu.Lock()
	indexDir = dir
	indexMu.Unlock()
	if dir == "" {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > persistentIndexMaxIdle {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// persistentTree is worktreeTree through the worktree's persistent index; not
// ok when there is none to use (turned off, in use by another measurement,
// or git can't use it).
func persistentTree(worktree string) (string, bool) {
	indexMu.Lock()
	dir := indexDir
	st := indexState[worktree]
	if st == nil && dir != "" {
		st = &persistentIndex{}
		indexState[worktree] = st
	}
	indexMu.Unlock()
	if dir == "" || !st.mu.TryLock() {
		return "", false
	}
	defer st.mu.Unlock()
	sum := sha1.Sum([]byte(worktree))
	index := filepath.Join(dir, hex.EncodeToString(sum[:])+".index")
	src, err := run(worktree, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", false
	}
	var size int64 = -1
	if info, err := os.Stat(src); err == nil {
		size = info.Size()
	}
	_, statErr := os.Stat(index)
	if statErr != nil || size != st.srcSize || time.Since(st.seededAt) > persistentIndexMaxAge {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", false
		}
		_ = os.Remove(index)
		if copyIndex(worktree, index) != nil {
			return "", false
		}
		st.seededAt, st.srcSize = time.Now(), size
	}
	tree, err := stageTree(worktree, index)
	if err != nil {
		_ = os.Remove(index)
		return "", false
	}
	return tree, true
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
