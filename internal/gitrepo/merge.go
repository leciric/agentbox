package gitrepo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

// ErrMergeConflict is Merge's answer when branch and into change the same
// lines: nothing was merged, and Conflicts says where.
var ErrMergeConflict = errors.New("the branches conflict")

// MergeConflictError is ErrMergeConflict with the files that conflict.
type MergeConflictError struct {
	Branch, Into string
	Files        []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("%s and %s change the same lines in %s: merge %s into %s first and resolve them there",
		e.Branch, e.Into, strings.Join(e.Files, ", "), e.Into, e.Branch)
}

func (e *MergeConflictError) Unwrap() error { return ErrMergeConflict }

// Merged is what Merge did.
type Merged struct {
	Commit      string // where into is now
	FastForward bool   // into had nothing of its own, so it moved to branch
	UpToDate    bool   // branch was already in into: nothing changed
}

// Merge merges branch into the branch into, here on this machine with no
// remote involved: how a project with nowhere to push lands an agent's work.
//
// The merge is worked out without a working tree (git merge-tree), so a
// conflict leaves everything as it was and is answered as a
// MergeConflictError. Then into moves to the result: a fast-forward when it
// has no commits of its own, a merge commit otherwise. Where into is checked
// out, with git merge --ff-only there, so its files move with it; that
// checkout must have no changes to tracked files, which git would otherwise
// have to merge with the user's work. Where it's checked out nowhere, the ref
// moves in place, and only from the commit it was read at.
func (r Repo) Merge(branch, into, message string) (Merged, error) {
	// One git operation that moves branches at a time per repository: the
	// base sync fast-forwards the same branch this moves, and two merges at
	// once would each read into where the other left it.
	mu, _ := syncing.LoadOrStore(r.Root, new(sync.Mutex))
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	tip, err := r.ResolveCommit("refs/heads/" + branch)
	if err != nil {
		return Merged{}, fmt.Errorf("no branch %s: %w", branch, err)
	}
	base, err := r.ResolveCommit("refs/heads/" + into)
	if err != nil {
		return Merged{}, fmt.Errorf("no branch %s: %w", into, err)
	}
	if r.IsAncestor(tip, base) {
		return Merged{Commit: base, UpToDate: true}, nil
	}
	checkout, err := r.checkoutOf(into)
	if err != nil {
		return Merged{}, err
	}
	if checkout != "" {
		if _, err := os.Stat(checkout); err != nil {
			return Merged{}, fmt.Errorf("%s is checked out in %s, which is gone: run git worktree prune", into, checkout)
		}
		changes, err := run(checkout, "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return Merged{}, err
		}
		if changes != "" {
			return Merged{}, fmt.Errorf("%s has uncommitted changes on %s: commit or stash them first, so the merge doesn't mix with them", checkout, into)
		}
	}
	result, ff := tip, r.IsAncestor(base, tip)
	if !ff {
		if result, err = r.mergeCommit(branch, into, tip, base, message); err != nil {
			return Merged{}, err
		}
	}
	if checkout == "" {
		if _, err := run(r.Root, "update-ref", "-m", "agentbox: merge "+branch, "refs/heads/"+into, result, base); err != nil {
			return Merged{}, err
		}
	} else if _, err := run(checkout, "merge", "--ff-only", "--quiet", result); err != nil {
		return Merged{}, err
	}
	return Merged{Commit: result, FastForward: ff}, nil
}

// mergeCommit makes the merge commit of tip into base, with no working tree,
// and answers it, or a MergeConflictError.
func (r Repo) mergeCommit(branch, into, tip, base, message string) (string, error) {
	// Exit 1 is a conflict, which output answers with nothing: merge-tree
	// still printed the files, so it runs here.
	cmd := exec.Command("git", "-C", r.Root, "merge-tree", "--write-tree", "--name-only", "--no-messages", base, tip)
	b, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
		var files []string
		for _, f := range strings.Split(strings.TrimSpace(string(b)), "\n")[1:] {
			if f != "" && !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
		return "", &MergeConflictError{Branch: branch, Into: into, Files: files}
	}
	if err != nil {
		stderr := ""
		if exit != nil {
			stderr = strings.TrimSpace(string(exit.Stderr))
		}
		return "", &gitError{args: cmd.Args[3:], err: err, stderr: stderr}
	}
	out := string(b)
	tree := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if message == "" {
		message = fmt.Sprintf("Merge branch '%s' into %s", branch, into)
	}
	commit, err := output(r.Root, identityEnv(r.Root), "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", base, "-p", tip, "-m", message)
	return strings.TrimSpace(commit), err
}

// identityEnv is AgentBox's name and email for a commit, for what the user's
// git config doesn't set: a commit made for them shouldn't fail for want of one.
func identityEnv(root string) []string {
	var env []string
	for key, fallback := range map[string]string{"email": "agentbox@localhost", "name": "AgentBox"} {
		upper := strings.ToUpper(key)
		if configured, _ := run(root, "config", "user."+key); configured != "" || os.Getenv("GIT_AUTHOR_"+upper) != "" {
			continue
		}
		env = append(env, "GIT_AUTHOR_"+upper+"="+fallback, "GIT_COMMITTER_"+upper+"="+fallback)
	}
	return env
}
