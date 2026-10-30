package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/k8s"
)

// Workspace is the tree an agent works in inside its pod: the branch the master
// gave it, in the directory the pod's volume is mounted at. It is the pod's own
// checkout, not a worktree of a repository on the master, so every git command
// here runs in the pod.
type Workspace struct {
	// Dir is the checkout: /workspace unless the cluster says otherwise.
	Dir string
	// Branch is the branch the agent works on.
	Branch string
	// Base is where the branch starts when the repository has no such branch
	// yet, and origin/HEAD is used when it is empty.
	Base string
	// Log records what was left alone rather than changed, at debug level. A
	// nil Log discards it.
	Log func(format string, args ...any)
}

// gitEnv is the environment every git command here runs with. A pod has no one
// at a terminal to answer a credential prompt, so git is told never to ask;
// the credential itself arrives over the gateway from stage 3 on. The identity
// is the master's: a checkpoint is AgentBox's record of the tree, not a commit
// by whoever the agent's own git config names.
var gitEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_AUTHOR_NAME=AgentBox", "GIT_AUTHOR_EMAIL=agentbox@localhost",
	"GIT_COMMITTER_NAME=AgentBox", "GIT_COMMITTER_EMAIL=agentbox@localhost",
}

// Prepare puts the workspace on the branch the master named.
//
// The pod's volume is the truth for the agent's uncommitted work, so nothing
// here resets, cleans or moves a tree that has changes: the branch is created
// only in a tree that has nothing to lose, and a dirty tree that is not on the
// branch is left where it is and reported. A fetch that cannot reach the
// network is reported and not fatal, because a pod whose egress is down still
// has what its volume holds.
func (w Workspace) Prepare(ctx context.Context) error {
	if w.Dir == "" {
		return errors.New("the workspace has no directory")
	}
	if w.Branch == "" {
		return errors.New("the workspace has no branch")
	}
	if strings.HasPrefix(w.Branch, "-") {
		return fmt.Errorf("the branch %q starts with '-', which git would read as an option", w.Branch)
	}
	info, err := os.Stat(w.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the workspace %s isn't there: the pod's volume is not mounted", w.Dir)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("the workspace %s is not a directory", w.Dir)
	}
	if _, err := os.Lstat(filepath.Join(w.Dir, ".git")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A fresh pod has an empty volume and no credential to clone with
			// yet. It has to be reachable all the same: the clone arrives with
			// the git profile work, and a pod that exits before it dials the
			// master is a pod the cluster restarts forever.
			w.logf("workspace: %s holds no repository yet; serving without one until the master delivers the clone", w.Dir)
			return nil
		}
		return err
	}
	w.fetch(ctx)
	return w.checkout(ctx)
}

// fetch brings the remote's branches up to date when it can. It is bounded
// because the session waits for the workspace, and a pod with a broken route to
// the git server would otherwise never open one.
func (w Workspace) fetch(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := git(ctx, w.Dir, "fetch", "--prune", "origin"); err != nil {
		w.logf("workspace: not fetching: %v", err)
	}
}

// checkout leaves the tree on its branch without losing anything. It is the
// order of the checks that keeps that promise: a dirty tree never gets as far as
// a checkout or a branch creation.
func (w Workspace) checkout(ctx context.Context) error {
	if current, err := git(ctx, w.Dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && current == w.Branch {
		return nil
	}
	dirty, err := w.dirty(ctx)
	if err != nil {
		return err
	}
	if dirty {
		w.logf("workspace: %s has uncommitted changes; leaving the checkout as it is", w.Dir)
		return nil
	}
	if resolves(ctx, w.Dir, "refs/heads/"+w.Branch) {
		if _, err := git(ctx, w.Dir, "checkout", "--quiet", w.Branch); err != nil {
			return fmt.Errorf("checking out %s: %w", w.Branch, err)
		}
		return nil
	}
	// A branch the agent pushed before, on a fresh volume: the remote's copy is
	// where the work is, and starting from it makes the local branch track it.
	base := w.Base
	if resolves(ctx, w.Dir, "refs/remotes/origin/"+w.Branch) {
		base = "refs/remotes/origin/" + w.Branch
	}
	if base == "" {
		base = "origin/HEAD"
	}
	if !resolves(ctx, w.Dir, base) {
		return fmt.Errorf("%s has no branch %s, and %s is not a commit to start it from", w.Dir, w.Branch, base)
	}
	if _, err := git(ctx, w.Dir, "checkout", "--quiet", "-b", w.Branch, base); err != nil {
		return fmt.Errorf("creating %s from %s: %w", w.Branch, base, err)
	}
	return nil
}

// dirty reports whether the tree has uncommitted changes or untracked files.
func (w Workspace) dirty(ctx context.Context) (bool, error) {
	out, err := git(ctx, w.Dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("reading whether the workspace has changes: %w", err)
	}
	return out != "", nil
}

// Checkpoint commits what the workspace has, untracked files included, as one
// commit on the branch, and returns its hash. When there is nothing to commit
// it returns the branch's head instead: a caller that points a checkpoint ref at
// the answer always has a commit, and no empty commit is made. The index is the
// tree's own, so the commit is what the agent would see had it run `git add -A`
// and committed itself.
func (w Workspace) Checkpoint(ctx context.Context, message string) (string, error) {
	if w.Dir == "" {
		return "", errors.New("the workspace has no directory")
	}
	if _, err := git(ctx, w.Dir, "add", "-A"); err != nil {
		return "", fmt.Errorf("staging the workspace: %w", err)
	}
	changed, err := w.staged(ctx)
	if err != nil {
		return "", err
	}
	head, err := git(ctx, w.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading the branch: %w", err)
	}
	if !changed {
		return head, nil
	}
	if message == "" {
		message = "agentbox checkpoint"
	}
	// The signing is turned off here rather than in gitEnv because it is an
	// option of this command only: the pod has no key, and a checkpoint that
	// waits on a pinentry prompt is a checkpoint that never lands.
	if _, err := git(ctx, w.Dir, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", message); err != nil {
		return "", fmt.Errorf("committing the workspace: %w", err)
	}
	commit, err := git(ctx, w.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading the checkpoint: %w", err)
	}
	return commit, nil
}

// staged reports whether the index differs from HEAD, which is what a commit
// would record. git answers "no" with 0 and "yes" with 1; anything else is git
// failing.
func (w Workspace) staged(ctx context.Context) (bool, error) {
	_, err := git(ctx, w.Dir, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var failed *gitError
	if errors.As(err, &failed) && failed.exitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("reading what the workspace staged: %w", err)
}

// writeTarget resolves a path the master wants written and refuses one that
// leaves the directories the pod's agent owns: the workspace it works in, its
// home, and agentd's run directory. Every root is checked the same way and after
// its symlinks are resolved, so neither a ".." nor a symlinked parent can write
// through one. The directory the file goes in is the write's to make when it
// isn't there, which is how the master sets up the home a pod starts without.
// The file is refused when it is a symlink, so a write cannot be turned into a
// write elsewhere by something the agent made earlier.
func (w Workspace) writeTarget(name string) (string, error) {
	if name == "" {
		return "", errors.New("a write needs a path")
	}
	workspace, err := filepath.EvalSymlinks(w.Dir)
	if err != nil {
		return "", fmt.Errorf("the workspace: %w", err)
	}
	target := filepath.Clean(name)
	if !filepath.IsAbs(name) {
		// A relative path is the workspace's. The agent's home is named whole,
		// which is how the master sets an agent up.
		target = filepath.Clean(filepath.Join(workspace, name))
		if !within(workspace, target) {
			return "", fmt.Errorf("%s is outside the workspace %s", name, workspace)
		}
	}
	roots := w.writeRoots(workspace)
	target, err = writeParent(target, roots)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink, and a write does not follow one", target)
	}
	return target, nil
}

// writeParent is the file target resolves to, with the directories it goes in
// checked after their symlinks. The deepest directory that is there is the one
// checked, so a parent the write itself makes is refused only when the
// directory it would make it in is outside the roots.
func writeParent(target string, roots []writeRoot) (string, error) {
	dir, rest := filepath.Dir(target), ""
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !withinAny(roots, resolved) {
				return "", fmt.Errorf("%s is outside every directory agentd writes in: %s", target, rootNames(roots))
			}
			return filepath.Join(resolved, rest, filepath.Base(target)), nil
		}
		// A directory that isn't there is one the write makes, as long as the
		// directory above it is the pod's: the image's home holds no .config or
		// .claude, and every file in them is the master's.
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("the directory %s the write is for: %w", dir, err)
		}
		above := filepath.Dir(dir)
		if above == dir {
			return "", fmt.Errorf("the directory %s the write is for: %w", dir, err)
		}
		dir, rest = above, filepath.Join(filepath.Base(dir), rest)
	}
}

// writeRoot is one directory the master may write in: the way the pod names it,
// and where that is once its symlinks are resolved. A directory that isn't there
// has no resolved path, because nothing can be written under it.
type writeRoot struct {
	dir      string
	resolved string
}

// writeRoots are the directories a pod's agent owns: the workspace and the pod
// user's home, both on the volume the pod mounts, and agentd's own run
// directory, the tmpfs the credential it is handed arrives in.
func (w Workspace) writeRoots(workspace string) []writeRoot {
	roots := []writeRoot{{dir: w.Dir, resolved: workspace}}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, writeRoot{dir: home, resolved: resolveDir(home)})
	}
	roots = append(roots, writeRoot{dir: k8s.RunDir, resolved: resolveDir(k8s.RunDir)})
	return roots
}

// resolveDir is a directory after its symlinks, or "" when it isn't there.
func resolveDir(dir string) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	return resolved
}

// withinAny reports whether path is one of the roots, or under one.
func withinAny(roots []writeRoot, path string) bool {
	for _, root := range roots {
		if root.resolved != "" && within(root.resolved, path) {
			return true
		}
	}
	return false
}

// rootNames names the roots of a refusal, so the master can see where a write
// may go.
func rootNames(roots []writeRoot) string {
	dirs := make([]string, 0, len(roots))
	for _, root := range roots {
		dirs = append(dirs, root.dir)
	}
	return strings.Join(dirs, ", ")
}

// within reports whether path is root or under it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// logf reports what Prepare left alone, and a fetch it could not make.
func (w Workspace) logf(format string, args ...any) {
	if w.Log != nil {
		w.Log(format, args...)
	}
}

// resolves reports whether rev names a commit in the workspace.
func resolves(ctx context.Context, dir, rev string) bool {
	_, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

// git runs one git command in dir and returns what it printed, trimmed.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), gitEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &gitError{args: args, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return strings.TrimSpace(string(out)), nil
}

// gitError is a git command that failed, with what it said. A caller reads the
// exit code to tell a git "no" from a git failure.
type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// exitCode is git's own, or -1 when it never ran.
func (e *gitError) exitCode() int {
	var exit *exec.ExitError
	if errors.As(e.err, &exit) {
		return exit.ExitCode()
	}
	return -1
}
