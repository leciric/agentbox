package worktreefs

// A past agent's files: its machine and worktree are gone, but its branch
// usually stays in the project's repository, so the same listing, reading and
// status are served from a commit instead of the disk. A commit has no
// ignored or untracked files, and doesn't change, so nothing is watched.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"agentbox/internal/api"
)

// ErrNoBranch is a branch that is in neither the repository nor its origin.
var ErrNoBranch = errors.New("branch not found")

// BranchCommit is the commit branch points at in the repository at root: its
// local branch, or origin's when the local one was deleted (an agent's branch
// is, once it was merged or pushed).
func BranchCommit(root, branch string) (string, error) {
	if branch != "" && !strings.HasPrefix(branch, "-") {
		for _, ref := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
			if c, err := commitOf(root, ref); err == nil {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoBranch, branch)
}

func commitOf(root, rev string) (string, error) {
	out, err := git(root, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// BaseAt is what commit's changes are measured against: baseCommit when the
// repository has it, else where commit forked from baseRef (or origin's).
// "" when neither is known.
func BaseAt(root, baseCommit, baseRef, commit string) string {
	if baseCommit != "" {
		if c, err := commitOf(root, baseCommit); err == nil {
			return c
		}
	}
	if baseRef == "" || strings.HasPrefix(baseRef, "-") {
		return ""
	}
	for _, ref := range []string{baseRef, "refs/remotes/origin/" + baseRef} {
		if out, err := git(root, nil, "merge-base", ref, commit); err == nil {
			if c := strings.TrimSpace(out); c != "" {
				return c
			}
		}
	}
	return ""
}

// object is what git finds at a path of a commit, its symlinks followed.
type object struct {
	oid, kind string // kind: blob, tree or commit (a submodule)
	size      int64
}

// lookup finds each of paths (relative to the commit's root) at commit,
// following symlinks as long as they stay inside the commit's tree. A path
// that isn't there is fs.ErrNotExist; one whose link leads outside is
// ErrOutside.
func lookup(root, commit string, paths []string) ([]object, []error, error) {
	objs := make([]object, len(paths))
	errs := make([]error, len(paths))
	var in strings.Builder
	for i, p := range paths {
		if strings.ContainsAny(p, "\n\r") { // one line per path
			errs[i] = fs.ErrNotExist
			continue
		}
		in.WriteString(commit + ":" + p + "\n")
	}
	out, err := git(root, strings.NewReader(in.String()), "cat-file", "--batch-check", "--follow-symlinks")
	if err != nil {
		return nil, nil, err
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for i := range paths {
		if errs[i] != nil {
			continue
		}
		if !sc.Scan() {
			return nil, nil, errors.New("git cat-file answered short")
		}
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 2 && f[1] == "missing":
			errs[i] = fs.ErrNotExist
		case len(f) == 2 && (f[0] == "symlink" || f[0] == "dangling" || f[0] == "loop" || f[0] == "notdir"):
			sc.Scan() // the name or the link that follows
			if f[0] == "symlink" {
				errs[i] = ErrOutside
			} else {
				errs[i] = fs.ErrNotExist
			}
		case len(f) == 3:
			size, _ := strconv.ParseInt(f[2], 10, 64)
			objs[i] = object{oid: f[0], kind: f[1], size: size}
		default:
			return nil, nil, fmt.Errorf("git cat-file answered %q", sc.Text())
		}
	}
	return objs, errs, nil
}

func lookupOne(root, commit, rel string) (object, error) {
	objs, errs, err := lookup(root, commit, []string{rel})
	if err != nil {
		return object{}, err
	}
	if errs[0] != nil {
		return object{}, fmt.Errorf("%s: %w", rel, errs[0])
	}
	return objs[0], nil
}

// ListAt is List for a folder of commit in the repository at root.
// Nothing in a commit is ignored, so there is no withIgnored.
func ListAt(root, commit, dir string) (api.FileList, error) {
	rel, err := Rel(root, dir)
	if err != nil {
		return api.FileList{}, err
	}
	out := api.FileList{Path: rel, Entries: []api.FileEntry{}}
	tree := commit + "^{tree}"
	if rel != "" {
		o, err := lookupOne(root, commit, rel)
		if err != nil {
			return out, err
		}
		switch o.kind {
		case "commit":
			return out, nil // a submodule: its files aren't in this repository
		case "tree":
			tree = o.oid
		default:
			return out, fmt.Errorf("%s is not a folder", rel)
		}
	}
	ls, err := git(root, nil, "ls-tree", "-z", "--long", "--end-of-options", tree)
	if err != nil {
		return out, err
	}
	var links []int
	for _, line := range strings.Split(ls, "\x00") {
		// <mode> SP <type> SP <object> SP+ <size> TAB <name>
		meta, name, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 || (rel == "" && name == ".git") {
			continue
		}
		e := api.FileEntry{Name: name, Path: path.Join(rel, name)}
		switch {
		case f[0] == "120000":
			e.Symlink = true
			links = append(links, len(out.Entries))
		case f[1] == "tree", f[1] == "commit":
			e.Dir = true
		default:
			e.Size, _ = strconv.ParseInt(f[3], 10, 64)
		}
		out.Entries = append(out.Entries, e)
	}
	// A link to a folder inside the commit opens like one; any other link is
	// a file, which ReadAt refuses when it leads outside.
	if len(links) > 0 {
		paths := make([]string, len(links))
		for i, j := range links {
			paths[i] = out.Entries[j].Path
		}
		if objs, errs, err := lookup(root, commit, paths); err == nil {
			for i, j := range links {
				if errs[i] == nil {
					out.Entries[j].Dir = objs[i].kind == "tree"
					if objs[i].kind == "blob" {
						out.Entries[j].Size = objs[i].size
					}
				}
			}
		}
	}
	sortAndCap(&out)
	return out, nil
}

// ReadAt is Read for a file of commit in the repository at root.
func ReadAt(root, commit, p string, full bool) (api.FileContent, error) {
	rel, err := Rel(root, p)
	if err != nil {
		return api.FileContent{}, err
	}
	if rel == "" {
		return api.FileContent{}, errors.New("the root is a folder")
	}
	o, err := lookupOne(root, commit, rel)
	if err != nil {
		return api.FileContent{}, err
	}
	if o.kind != "blob" {
		return api.FileContent{}, fmt.Errorf("%s is a folder", rel)
	}
	return content(rel, o.size, full, func(limit int64) ([]byte, error) {
		return blob(root, o.oid, limit)
	})
}

// blob reads up to limit bytes of a blob, without waiting for the rest.
func blob(root, oid string, limit int64) ([]byte, error) {
	cmd := exec.Command("git", "-C", root, "cat-file", "blob", oid)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(stdout, limit))
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return data, err
}

// FilesAt is every file of commit, up to limit, for a fuzzy find.
func FilesAt(root, commit string, limit int) ([]string, bool, error) {
	out, err := git(root, nil, "ls-tree", "-r", "-z", "--name-only", "--end-of-options", commit)
	if err != nil {
		return nil, false, err
	}
	files := []string{}
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

// StatusAt is what changed from base to commit, a rename as its deletion
// and an addition. A base of "" is no changes.
func StatusAt(root, base, commit string) (map[string]string, bool, error) {
	changes := map[string]string{}
	if base == "" {
		return changes, false, nil
	}
	out, err := git(root, nil, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", "--end-of-options", base, commit, "--")
	if err != nil {
		return nil, false, err
	}
	truncated := false
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "" {
			continue
		}
		if len(changes) >= ChangesLimit {
			truncated = true
			break
		}
		switch fields[i] {
		case "A":
			changes[fields[i+1]] = api.FileAdded
		case "D":
			changes[fields[i+1]] = api.FileDeleted
		default:
			changes[fields[i+1]] = api.FileModified
		}
	}
	return changes, truncated, nil
}
