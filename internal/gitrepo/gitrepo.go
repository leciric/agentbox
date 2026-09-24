// Package gitrepo wraps the git commands AgentBox needs.
package gitrepo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Repo is the main checkout of a git repository.
type Repo struct {
	Root   string // main checkout directory
	GitDir string // the shared .git directory
}

// Open finds the repository containing path. Inside a linked worktree it
// resolves the main checkout.
func Open(path string) (Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Repo{}, err
	}
	common, err := run(abs, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Repo{}, fmt.Errorf("%s is not inside a git repository", abs)
	}
	if filepath.Base(common) != ".git" {
		return Repo{}, fmt.Errorf("%s: bare repositories and submodules are not supported", abs)
	}
	return Repo{Root: filepath.Dir(common), GitDir: common}, nil
}

// Clone copies src into dest, a folder that mustn't exist yet, and opens the
// copy: in WSL, how a repository on a Windows drive gets onto the distro's own
// disk (D91). It's a clone of a local path, so no network, and it copies only
// what's committed. src becomes the copy's `windows` remote, to fetch from, and
// src's own origin, if it has one, is the copy's origin, so pushing from the
// copy goes where pushing from src did.
func Clone(src Repo, dest string) (Repo, error) {
	if _, err := os.Lstat(dest); err == nil {
		return Repo{}, fmt.Errorf("%s already exists: move it away, or add it as it is", dest)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Repo{}, err
	}
	if _, err := run(parent, "clone", "--quiet", "--origin", "windows", "--", src.Root, dest); err != nil {
		return Repo{}, err
	}
	if origin, err := run(src.Root, "remote", "get-url", "origin"); err == nil && origin != "" {
		if _, err := run(dest, "remote", "add", "origin", origin); err != nil {
			return Repo{}, err
		}
	}
	return Open(dest)
}

// HasCommits reports whether HEAD points to a commit.
func (r Repo) HasCommits() bool {
	_, err := run(r.Root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	return err == nil
}

// CurrentBranch returns the branch checked out in the main checkout, or
// "HEAD" when it is detached.
func (r Repo) CurrentBranch() string {
	if branch, err := run(r.Root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		return branch
	}
	return "HEAD"
}

// ResolveCommit returns the commit a branch, tag or revision points to.
func (r Repo) ResolveCommit(rev string) (string, error) {
	commit, err := run(r.Root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q is not a commit in %s", rev, r.Root)
	}
	return commit, nil
}

func (r Repo) BranchExists(branch string) bool {
	_, err := run(r.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Branches lists local branches in a namespace, like "agentbox" for agentbox/*.
func (r Repo) Branches(namespace string) ([]string, error) {
	out, err := run(r.Root, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+namespace+"/")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// BranchesStartingWith lists the branches whose name is prefix followed by one
// more path component, like agentbox/agent-01 for "agentbox/": the local ones,
// and every remote's as last fetched, with the remote's name taken off. A
// branch that exists in several of them is listed once.
//
// The remotes' count because a branch pushed by someone else sharing the
// repository is theirs even if it was never checked out here.
func (r Repo) BranchesStartingWith(prefix string) ([]string, error) {
	// A ref can't hold '*', so the prefix is never read as a pattern; and '*'
	// doesn't match '/' in for-each-ref, which keeps this to one component.
	patterns := []string{"refs/heads/" + prefix + "*"}
	remotes, err := run(r.Root, "remote")
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, remote := range strings.Fields(remotes) {
		root := "refs/remotes/" + remote + "/"
		roots = append(roots, root)
		patterns = append(patterns, root+prefix+"*")
	}
	out, err := run(r.Root, append([]string{"for-each-ref", "--format=%(refname)"}, patterns...)...)
	if err != nil || out == "" {
		return nil, err
	}
	seen := map[string]bool{}
	var branches []string
	for _, ref := range strings.Split(out, "\n") {
		name, ok := strings.CutPrefix(ref, "refs/heads/")
		for _, root := range roots {
			if ok {
				break
			}
			name, ok = strings.CutPrefix(ref, root)
		}
		// refs/remotes/<remote>/HEAD points at a branch, and isn't one.
		if !ok || name == "HEAD" || seen[name] {
			continue
		}
		seen[name] = true
		branches = append(branches, name)
	}
	return branches, nil
}

// CheckBranchPrefix reports whether prefix can start a branch name: whether
// prefix+"agent-01" passes git check-ref-format --branch. An empty prefix is
// allowed, and so is one of several components, like "thiago/agentbox/".
func CheckBranchPrefix(prefix string) error {
	name := prefix + "agent-01"
	bad := func(why string) error {
		return fmt.Errorf("%q can't start a branch name: %s", prefix, why)
	}
	switch {
	case strings.HasPrefix(name, "-"):
		return bad("a branch can't start with '-'")
	case strings.HasPrefix(name, "/"):
		return bad("it can't start with '/'")
	case strings.Contains(name, "//"):
		return bad("it can't have '//'")
	case strings.Contains(name, ".."):
		return bad("it can't have '..'")
	case strings.Contains(name, "@{"):
		return bad("it can't have '@{'")
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(" ~^:?*[\\", c) {
			return bad(fmt.Sprintf("it can't have %q", c))
		}
	}
	for _, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") {
			return bad("no part of it can start with '.'")
		}
		if strings.HasSuffix(component, ".lock") {
			return bad("no part of it can end with '.lock'")
		}
	}
	return nil
}

// BranchInTheWay returns a local branch that stops any branch starting with
// prefix from being made, or "" if none does: git keeps branches as paths, so
// with a branch thiago there can be no thiago/agent-01.
func (r Repo) BranchInTheWay(prefix string) string {
	for i, c := range prefix {
		if c == '/' && r.BranchExists(prefix[:i]) {
			return prefix[:i]
		}
	}
	return ""
}

// AddWorktree creates a worktree at path on a new branch starting at commit.
func (r Repo) AddWorktree(path, branch, commit string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := run(r.Root, "worktree", "add", "--quiet", "-b", branch, path, commit)
	return err
}

// AddWorktreeDetached creates a worktree at path with a detached HEAD at
// commit. Nothing is committed there and no branch is created, so several
// worktrees can stand on the same commit as a branch the main checkout holds.
func (r Repo) AddWorktreeDetached(path, commit string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := run(r.Root, "worktree", "add", "--quiet", "--detach", path, commit)
	return err
}

// Fetch moves a detached worktree to commit, discarding anything left in it.
func MoveWorktree(worktree, commit string) error {
	_, err := run(worktree, "checkout", "--quiet", "--force", "--detach", commit)
	return err
}

// RemoveWorktree removes the worktree at path, discarding uncommitted changes.
// A worktree git no longer knows, because its entry under .git/worktrees was
// pruned, is only a directory now, and is removed as one.
func (r Repo) RemoveWorktree(path string) error {
	if _, err := os.Stat(path); err == nil {
		if !r.HasWorktree(path) {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		} else if _, err := run(r.Root, "worktree", "remove", "--force", path); err != nil {
			return err
		}
	}
	_, err := run(r.Root, "worktree", "prune")
	return err
}

// HasWorktree reports whether git knows path as a worktree of this repository.
// A `git worktree prune` run where path doesn't exist, like inside an agent's
// machine, unregisters it: its files and .git pointer stay, but every git
// command there fails with "not a git repository".
func (r Repo) HasWorktree(path string) bool {
	common, err := run(path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return err == nil && common == r.GitDir
}

func (r Repo) DeleteBranch(branch string) error {
	_, err := run(r.Root, "branch", "--quiet", "-D", branch)
	return err
}

// EnvFiles lists gitignored env files in the main checkout, like .env or
// apps/api/.env.local. New worktrees don't have them, so agents need copies.
func (r Repo) EnvFiles() ([]string, error) {
	out, err := output(r.Root, nil, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		// --directory collapses ignored directories (node_modules/), so their contents are skipped.
		if p != "" && !strings.HasSuffix(p, "/") && isEnvFile(p) {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}

// ListFiles lists a worktree's tracked files plus its untracked-but-not-ignored
// ones, relative to its root, for @ mentions in the composer. It stops at
// limit entries and reports whether more exist.
func ListFiles(worktree string, limit int) (files []string, truncated bool, err error) {
	out, err := output(worktree, nil, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false, err
	}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		if len(files) >= limit {
			return files, true, nil
		}
		files = append(files, p)
	}
	return files, false, nil
}

func isEnvFile(path string) bool {
	base := filepath.Base(path)
	if base != ".env" && !strings.HasPrefix(base, ".env.") {
		return false
	}
	for _, suffix := range []string{".example", ".sample", ".template", ".dist", ".bak", ".backup", ".old", ".orig"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	return true
}

// Dirty reports whether a worktree has uncommitted changes or untracked files.
func Dirty(worktree string) (bool, error) {
	out, err := run(worktree, "status", "--porcelain")
	return out != "", err
}

// DiffWorktree diffs base against the worktree's current files, including
// uncommitted and untracked ones, without touching the worktree's index.
func DiffWorktree(worktree, base string, stat bool, paths ...string) (string, error) {
	tree, err := worktreeTree(worktree)
	if err != nil {
		return "", err
	}
	args := []string{"diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, base, tree)
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	return output(worktree, nil, args...)
}

func run(dir string, args ...string) (string, error) {
	out, err := output(dir, nil, args...)
	return strings.TrimSpace(out), err
}

func output(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
