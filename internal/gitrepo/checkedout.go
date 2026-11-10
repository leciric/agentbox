package gitrepo

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
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
// process, since it runs with every look at an agent's pull requests. A file
// that isn't there is an answer (no worktree, no reflog); one that is there
// and can't be read is an error, as the branches it would have named are
// still the worktree's.
func CheckedOutSince(worktree string, since time.Time) ([]string, error) {
	gitDir, err := worktreeGitDir(worktree)
	if gitDir == "" || err != nil {
		return nil, err
	}
	var seen []string
	add := func(branch string) {
		if branch != "" && branch != "HEAD" && !isHex(branch) && !slices.Contains(seen, branch) {
			seen = append(seen, branch)
		}
	}
	head, err := readFile(filepath.Join(gitDir, "HEAD"))
	switch {
	case err == nil:
		add(strings.TrimPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/"))
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	lines, err := reflogMessages(filepath.Join(gitDir, "logs", "HEAD"))
	if err != nil {
		return nil, err
	}
	for i := len(lines) - 1; i >= 0; i-- {
		// "checkout: moving from <a> to <b>", which switch writes too. A
		// branch name can't hold a space.
		from, to, ok := strings.Cut(strings.TrimPrefix(lines[i], "checkout: moving from "), " to ")
		if ok && strings.HasPrefix(lines[i], "checkout: ") {
			add(to)
			add(from)
		}
	}
	common, err := commonDir(gitDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, branch := range seen {
		created, ok, err := branchCreated(common, branch)
		if err != nil {
			return nil, err
		}
		if ok && !created.Before(since.Truncate(time.Second)) {
			out = append(out, branch)
		}
	}
	return out, nil
}

// worktreeGitDir is the private git directory of a linked worktree, from the
// .git file in it; "" when it has none.
func worktreeGitDir(worktree string) (string, error) {
	path := filepath.Join(worktree, ".git")
	if info, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) || (err == nil && info.IsDir()) {
		return "", nil // no worktree, or the main checkout
	}
	b, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return "", nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktree, dir)
	}
	return dir, nil
}

// commonDir is the repository's shared git directory, where branches and
// their reflogs are, for a worktree's private one.
func commonDir(gitDir string) (string, error) {
	b, err := readFile(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return gitDir, nil
	}
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(b))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(gitDir, dir)
	}
	return filepath.Clean(dir), nil
}

// branchCreated is when a branch's reflog begins: when it was created, as
// long as the reflog has never been expired, which takes 90 days by default.
func branchCreated(common, branch string) (time.Time, bool, error) {
	f, err := openFile(filepath.Join(common, "logs", "refs", "heads", filepath.FromSlash(branch)))
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return time.Time{}, false, sc.Err()
	}
	// "<old> <new> <name> <email> <unix seconds> <zone>\t<message>"
	who, _, _ := strings.Cut(sc.Text(), "\t")
	fields := strings.Fields(who)
	if len(fields) < 2 {
		return time.Time{}, false, nil
	}
	secs, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return time.Time{}, false, nil
	}
	return time.Unix(secs, 0), true, nil
}

// reflogMessages is the message of every entry of a reflog, oldest first;
// none when there is no reflog.
func reflogMessages(path string) ([]string, error) {
	b, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.Lines(string(b)) {
		if _, msg, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\t"); ok {
			out = append(out, msg)
		}
	}
	return out, nil
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

// WorktreeStamp changes whenever a linked worktree's HEAD, index or checked-out
// branch's tip file is touched: a cheap few stats that tell whether a diff
// measured earlier can still stand, with no git process. "" when it can't be
// read (no worktree, the main checkout), which never matches a stamp.
func WorktreeStamp(worktree string) string {
	gitDir, err := worktreeGitDir(worktree)
	if err != nil || gitDir == "" {
		return ""
	}
	var b strings.Builder
	stamp := func(path string) {
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", filepath.Base(path), info.ModTime().UnixNano(), info.Size())
		} else {
			b.WriteString("-;")
		}
	}
	stamp(filepath.Join(gitDir, "HEAD"))
	stamp(filepath.Join(gitDir, "index"))
	if head, err := readFile(filepath.Join(gitDir, "HEAD")); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: "); ok {
			if common, err := commonDir(gitDir); err == nil {
				stamp(filepath.Join(common, filepath.FromSlash(ref)))
				stamp(filepath.Join(common, "packed-refs"))
			}
		}
	}
	return b.String()
}
