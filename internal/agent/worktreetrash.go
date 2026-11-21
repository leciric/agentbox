package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"agentbox/internal/gitrepo"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// trashWorktree moves a's worktree into Paths.WorktreeTrash, so a destroy
// doesn't wait for its files to be deleted: on a Mac each one is a round trip
// through the VM's share, half a minute for a fresh worktree and minutes for
// one with a build in it. The trash is the one beside the worktrees directory
// a's worktree is in: on the VM's disk, or for an agent made before worktrees
// moved there, on the share. It reports false when it couldn't, on another
// disk say, and the worktree is deleted where it is.
func (m *Manager) trashWorktree(repo gitrepo.Repo, a state.Agent) bool {
	trash := trashFor(m.Paths, a.Worktree)
	if trash == "" {
		return false
	}
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return false
	}
	// git's prune drops a moved worktree's entry, but never a locked one's.
	_ = repo.UnlockWorktree(a.Worktree)
	dst := filepath.Join(trash, a.Project+"-"+a.Name+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	return os.Rename(a.Worktree, dst) == nil
}

// EmptyWorktreeTrash deletes what trashWorktree moved aside, and what a
// daemon that stopped meanwhile left there, until ctx is done, and says how
// many it deleted. wait holds each one back while the shared home is short
// of files (ShareRoom).
func EmptyWorktreeTrash(ctx context.Context, p paths.Paths, wait func(ctx context.Context, what string), logf func(format string, args ...any)) (deleted int) {
	trashes, _ := p.WorktreeTrashes()
	for _, trash := range trashes {
		worktrees, _ := os.ReadDir(trash)
		for _, w := range worktrees {
			if ctx.Err() != nil {
				return deleted
			}
			wait(ctx, "deleting a destroyed agent's worktree")
			if err := removeTreeUntil(ctx, filepath.Join(trash, w.Name())); err != nil {
				if ctx.Err() == nil {
					logf("Deleting a destroyed agent's worktree: %v", err)
				}
				continue
			}
			deleted++
		}
	}
	return deleted
}

// trashFor is the trash beside the worktrees directory that has worktree, or
// "" when it's in none of them.
func trashFor(p paths.Paths, worktree string) string {
	trashes, dirs := p.WorktreeTrashes()
	for i, dir := range dirs {
		if rel, err := filepath.Rel(dir, filepath.Clean(worktree)); err == nil && filepath.IsLocal(rel) {
			return trashes[i]
		}
	}
	return ""
}

// removeTreeUntil is os.RemoveAll that stops when ctx is done, so a daemon
// stopping doesn't wait for a whole build directory. Like RemoveAll, it
// removes before it reads: an empty directory it can't read, where a
// machine's node_modules were mounted, goes all the same.
func removeTreeUntil(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if info, lerr := os.Lstat(path); lerr != nil || !info.IsDir() {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		if err := removeTreeUntil(ctx, filepath.Join(path, e.Name())); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return first
	}
	return os.Remove(path)
}
