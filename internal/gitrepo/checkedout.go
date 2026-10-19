package gitrepo

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CheckedOutSince is every branch a linked worktree has had checked out,
// newest first, as its HEAD's reflog remembers them, that was created at or
// after since. It is how an agent's other branches are found: one that
// started a second pull request with `git switch -c` in its worktree left that
// in the worktree's own reflog, which nothing else writes to.
//
// A branch created before since — a base branch such as develop, or another
// agent's, checked out to look at — isn't one the worktree made, and is left
// out; so is one whose creation git didn't log. It reads files only, no git
// process, since it runs with every look at an agent's pull requests; what it
// can't read it leaves out.
func CheckedOutSince(worktree string, since time.Time) []string {
	gitDir := worktreeGitDir(worktree)
	if gitDir == "" {
		return nil
	}
	var seen []string
	add := func(branch string) {
		if branch != "" && branch != "HEAD" && !isHex(branch) && !slices.Contains(seen, branch) {
			seen = append(seen, branch)
		}
	}
	if head, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
		add(strings.TrimPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/"))
	}
	lines := reflogMessages(filepath.Join(gitDir, "logs", "HEAD"))
	for i := len(lines) - 1; i >= 0; i-- {
		// "checkout: moving from <a> to <b>", which switch writes too. A
		// branch name can't hold a space.
		from, to, ok := strings.Cut(strings.TrimPrefix(lines[i], "checkout: moving from "), " to ")
		if ok && strings.HasPrefix(lines[i], "checkout: ") {
			add(to)
			add(from)
		}
	}
	common := commonDir(gitDir)
	var out []string
	for _, branch := range seen {
		if created, ok := branchCreated(common, branch); ok && !created.Before(since.Truncate(time.Second)) {
			out = append(out, branch)
		}
	}
	return out
}

// worktreeGitDir is the private git directory of a linked worktree, from the
// .git file in it; "" when it has none.
func worktreeGitDir(worktree string) string {
	b, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return ""
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktree, dir)
	}
	return dir
}

// commonDir is the repository's shared git directory, where branches and
// their reflogs are, for a worktree's private one.
func commonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	dir := strings.TrimSpace(string(b))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(gitDir, dir)
	}
	return filepath.Clean(dir)
}

// branchCreated is when a branch's reflog begins: when it was created, as
// long as the reflog has never been expired, which takes 90 days by default.
func branchCreated(common, branch string) (time.Time, bool) {
	f, err := os.Open(filepath.Join(common, "logs", "refs", "heads", filepath.FromSlash(branch)))
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return time.Time{}, false
	}
	// "<old> <new> <name> <email> <unix seconds> <zone>\t<message>"
	who, _, _ := strings.Cut(sc.Text(), "\t")
	fields := strings.Fields(who)
	if len(fields) < 2 {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// reflogMessages is the message of every entry of a reflog, oldest first.
func reflogMessages(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.Lines(string(b)) {
		if _, msg, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\t"); ok {
			out = append(out, msg)
		}
	}
	return out
}

// isHex reports whether s is a commit id, which is where a detached HEAD
// moves to.
func isHex(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
