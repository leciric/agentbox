package gitrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CommitFiles commits files (path → contents, relative to the repository's
// root) and the removal of remove on branch, on top of start, without
// touching any checkout: a temporary index is built from start's tree, the
// files are written as blobs into it, and the commit is made from that index
// and moved onto branch. The user's working tree, their index and whatever
// they have checked out stay as they were.
//
// changed is false, and commit is start, when the tree would come out the same
// as start's: nothing is committed then. branch must not be checked out
// anywhere, since moving a checked-out branch under its worktree would leave
// that worktree's files describing the wrong commit.
func (r Repo) CommitFiles(branch, start string, files map[string][]byte, remove []string, message string) (commit string, changed bool, err error) {
	if dir, err := r.checkoutOf(branch); err != nil {
		return "", false, err
	} else if dir != "" {
		return "", false, fmt.Errorf("%s is checked out in %s: switch that checkout to another branch first", branch, dir)
	}
	old, err := r.BranchTip(branch)
	if err != nil {
		return "", false, err
	}
	startCommit, err := r.ResolveCommit(start)
	if err != nil {
		return "", false, err
	}
	tmp, err := os.MkdirTemp("", "agentbox-commit-")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	git := func(args ...string) (string, error) {
		out, err := output(r.Root, env, args...)
		return strings.TrimSpace(out), err
	}
	if _, err := git("read-tree", startCommit); err != nil {
		return "", false, err
	}
	for path, data := range files {
		blob := filepath.Join(tmp, "blob")
		if err := os.WriteFile(blob, data, 0o600); err != nil {
			return "", false, err
		}
		sha, err := git("hash-object", "-w", "--", blob)
		if err != nil {
			return "", false, err
		}
		if _, err := git("update-index", "--add", "--cacheinfo", "100644,"+sha+","+filepath.ToSlash(path)); err != nil {
			return "", false, err
		}
	}
	for _, path := range remove {
		if _, err := git("update-index", "--force-remove", "--", filepath.ToSlash(path)); err != nil {
			return "", false, err
		}
	}
	tree, err := git("write-tree")
	if err != nil {
		return "", false, err
	}
	if was, err := git("rev-parse", startCommit+"^{tree}"); err == nil && was == tree {
		return startCommit, false, nil
	}
	// commit-tree takes who it is from the repository's config, like a
	// commit does; a repository with no identity anywhere gets AgentBox's
	// rather than failing on the user's behalf.
	commitEnv := env
	if name, _ := run(r.Root, "config", "user.name"); name == "" {
		commitEnv = append(commitEnv, "GIT_AUTHOR_NAME=AgentBox", "GIT_COMMITTER_NAME=AgentBox")
	}
	if email, _ := run(r.Root, "config", "user.email"); email == "" {
		commitEnv = append(commitEnv, "GIT_AUTHOR_EMAIL=agentbox@localhost", "GIT_COMMITTER_EMAIL=agentbox@localhost")
	}
	out, err := output(r.Root, commitEnv, "commit-tree", tree, "-p", startCommit, "-m", message)
	if err != nil {
		return "", false, err
	}
	commit = strings.TrimSpace(out)
	// The old value guards the move: a branch that moved meanwhile is left
	// where it went, and the share fails rather than dropping that commit.
	if old == "" {
		old = strings.Repeat("0", len(commit))
	}
	if _, err := run(r.Root, "update-ref", "-m", "agentbox: "+firstLine(message), "refs/heads/"+branch, commit, old); err != nil {
		return "", false, err
	}
	return commit, true, nil
}

// HasPath reports whether rev's tree has path, a file or a folder.
func (r Repo) HasPath(rev, path string) bool {
	_, err := run(r.Root, "cat-file", "-e", rev+":"+filepath.ToSlash(path))
	return err == nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
