package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// FetchTimeout is how long Fetch waits on a remote before giving up on it.
const FetchTimeout = 30 * time.Second

// Upstream is a local branch's copy on a remote.
type Upstream struct {
	Remote string // the remote's name, like origin
	Ref    string // its remote-tracking ref, like refs/remotes/origin/main
}

// UpstreamOf is branch's copy on a remote: the branch it tracks, or else
// origin's branch of the same name. It is there whether or not it has been
// fetched yet; ok is false when branch has neither, or tracks another local
// branch.
func (r Repo) UpstreamOf(branch string) (up Upstream, ok bool) {
	out, _ := run(r.Root, "for-each-ref", "--format=%(upstream:remotename) %(upstream)", "refs/heads/"+branch)
	if f := strings.Fields(out); len(f) == 2 && strings.HasPrefix(f[1], "refs/remotes/") {
		return Upstream{Remote: f[0], Ref: f[1]}, true
	}
	if out != "" {
		return Upstream{}, false // it tracks something, just not a remote
	}
	if _, err := run(r.Root, "config", "--get", "remote.origin.url"); err != nil {
		return Upstream{}, false
	}
	return Upstream{Remote: "origin", Ref: "refs/remotes/origin/" + branch}, true
}

// BaseBranch is the local branch a project's work is based on, and its copy
// on a remote: the branch origin's default one is named after (origin/HEAD,
// as git clone records it), or else main or master. ok is false when the
// repository has none of those with a copy on a remote.
func (r Repo) BaseBranch() (branch string, up Upstream, ok bool) {
	candidates := []string{"main", "master"}
	if def, err := run(r.Root, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		candidates = []string{strings.TrimPrefix(def, "refs/remotes/origin/")}
	}
	for _, branch := range candidates {
		if !r.BranchExists(branch) {
			continue
		}
		if up, ok := r.UpstreamOf(branch); ok {
			return branch, up, true
		}
	}
	return "", Upstream{}, false
}

// Fetch brings remote's branches up to date, as git fetch does, and gives up
// after FetchTimeout. It never asks for a password or a passphrase, since
// nobody is there to answer: a remote that needs one fails instead.
func (r Repo) Fetch(ctx context.Context, remote string) error {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	args := []string{"-C", r.Root, "fetch", "--quiet", "--no-write-fetch-head", remote}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never", "GCM_INTERACTIVE=never")
	// ssh, started by git, holds the output open after git is killed.
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("git fetch %s: no answer in %s", remote, FetchTimeout)
		}
		return fmt.Errorf("git fetch %s: %w: %s", remote, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Ahead is where up is when it is strictly ahead of branch — it has every
// commit branch has, and more — as last fetched. ok is false when either
// doesn't resolve, they are at the same commit, or branch has commits of its
// own.
func (r Repo) Ahead(branch string, up Upstream) (commit string, ok bool) {
	local, err := r.ResolveCommit("refs/heads/" + branch)
	if err != nil {
		return "", false
	}
	remote, err := r.ResolveCommit(up.Ref)
	if err != nil || remote == local || !r.IsAncestor(local, remote) {
		return "", false
	}
	return remote, true
}

// FastForward moves branch to its upstream when the upstream is strictly
// ahead of it, and says whether it moved. It never forces: a branch with
// commits of its own is left where it is. A branch checked out somewhere
// moves only when that checkout has no changes to tracked files, with git
// merge --ff-only there, so the files move with it; one checked out nowhere
// is moved in place, and only from the commit it was read at.
func (r Repo) FastForward(branch string, up Upstream) (bool, error) {
	remote, ok := r.Ahead(branch, up)
	if !ok {
		return false, nil
	}
	local, err := r.ResolveCommit("refs/heads/" + branch)
	if err != nil {
		return false, nil
	}
	checkout, err := r.checkoutOf(branch)
	if err != nil {
		return false, err
	}
	if checkout == "" {
		_, err := run(r.Root, "update-ref", "-m", "agentbox: fast-forward to "+up.Ref, "refs/heads/"+branch, remote, local)
		return err == nil, err
	}
	if _, err := os.Stat(checkout); err != nil {
		return false, nil // a worktree whose directory is gone: git would refuse anyway
	}
	changes, err := run(checkout, "status", "--porcelain", "--untracked-files=no")
	if err != nil || changes != "" {
		return false, err
	}
	if _, err := run(checkout, "merge", "--ff-only", "--quiet", remote); err != nil {
		return false, err
	}
	return true, nil
}

// Synced is what SyncBase did.
type Synced struct {
	Branch   string   // the base branch, "" when the repository has none to keep up to date
	Upstream Upstream // its copy on a remote
	Moved    bool     // whether it was fast-forwarded
}

// syncing keeps SyncBase to one at a time per repository: the daemon's own,
// every few minutes, and the one before an agent is created would otherwise
// fetch into the same refs at once.
var syncing sync.Map // Root → *sync.Mutex

// SyncBase fetches the remote of the repository's base branch (BaseBranch)
// and fast-forwards the branch to the remote's copy when it is strictly
// behind it, with FastForward's care for a checkout and a diverged branch.
func (r Repo) SyncBase(ctx context.Context) (Synced, error) {
	branch, up, ok := r.BaseBranch()
	if !ok {
		return Synced{}, nil
	}
	mu, _ := syncing.LoadOrStore(r.Root, new(sync.Mutex))
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	synced := Synced{Branch: branch, Upstream: up}
	if err := r.Fetch(ctx, up.Remote); err != nil {
		return synced, err
	}
	moved, err := r.FastForward(branch, up)
	synced.Moved = moved
	return synced, err
}

// checkoutOf is the directory of the worktree branch is checked out in, the
// main checkout included, or "" when it is checked out nowhere.
func (r Repo) checkoutOf(branch string) (string, error) {
	out, err := run(r.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var dir string
	for line := range strings.SplitSeq(out, "\n") {
		if d, ok := strings.CutPrefix(line, "worktree "); ok {
			dir = d
		} else if line == "branch refs/heads/"+branch {
			return dir, nil
		}
	}
	return "", nil
}
