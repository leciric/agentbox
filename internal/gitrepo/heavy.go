package gitrepo

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Skipped is a folder or file that a first commit leaves out although no
// .gitignore does: installed dependencies, caches and build output that are
// remade from what is committed, and files too big for a repository.
type Skipped struct {
	Path   string // relative to the repository's top, with / between parts
	Bytes  int64  // its size on disk, a folder's every file added up
	Reason string // "dependencies", "cache", "build-output" or "large-file"
}

// LargeFile is the size from which a file is left out of a first commit.
// GitHub refuses files over 100 MB on push, and warns from 50 MB; a repository
// that can't be pushed later is worse than one file left out and said so.
const LargeFile = 50 << 20

// heavyDirs are the folders a first commit leaves out wherever they are, by
// name, and why. They're remade by installing or building, are often far
// bigger than the code, and a repository with them committed is one nobody
// wants. A name that's also an ordinary source folder (build, dist, vendor,
// bin) isn't here unless something beside it says it's output (heavyBeside).
var heavyDirs = map[string]string{
	"node_modules":     "dependencies",
	"bower_components": "dependencies",
	".venv":            "dependencies",
	"__pycache__":      "cache",
	".pytest_cache":    "cache",
	".mypy_cache":      "cache",
	".ruff_cache":      "cache",
	".tox":             "cache",
	".gradle":          "cache",
	".parcel-cache":    "cache",
	".turbo":           "cache",
	".next":            "build-output",
	".nuxt":            "build-output",
	".svelte-kit":      "build-output",
	".dart_tool":       "build-output",
}

// heavyBeside is the folders that are output only when a file next to them
// says so: Cargo's and Maven's target, a Python venv by its pyvenv.cfg inside.
var heavyBeside = map[string][]string{
	"target": {"Cargo.toml", "pom.xml"},
}

// Heavy lists what a first commit of root would leave out on top of what its
// .gitignore (and the user's global excludes) already does, biggest first. It
// reads root and needn't be a repository yet: Create calls it before git init,
// and so does a caller that asks the user first.
func Heavy(root string) ([]Skipped, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var found []Skipped
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == abs {
				return err
			}
			return nil // a folder it can't read is git's to complain about
		}
		if path == abs {
			return nil
		}
		rel := filepath.ToSlash(must(filepath.Rel(abs, path)))
		switch {
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.IsDir():
			if reason, ok := heavyDir(path, d.Name()); ok {
				found = append(found, Skipped{Path: rel, Bytes: dirSize(path), Reason: reason})
				return filepath.SkipDir
			}
		case d.Type().IsRegular():
			if info, err := d.Info(); err == nil && info.Size() >= LargeFile {
				found = append(found, Skipped{Path: rel, Bytes: info.Size(), Reason: "large-file"})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	ignored, err := ignoredOf(abs, found)
	if err != nil {
		return nil, err
	}
	found = slices.DeleteFunc(found, func(s Skipped) bool { return ignored[s.Path] })
	// A large file inside a folder that's already left out isn't news.
	found = slices.DeleteFunc(found, func(s Skipped) bool {
		return slices.ContainsFunc(found, func(o Skipped) bool { return o.Reason != "large-file" && strings.HasPrefix(s.Path, o.Path+"/") })
	})
	slices.SortStableFunc(found, func(a, b Skipped) int {
		switch {
		case a.Bytes > b.Bytes:
			return -1
		case a.Bytes < b.Bytes:
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return found, nil
}

func heavyDir(path, name string) (string, bool) {
	if reason, ok := heavyDirs[name]; ok {
		return reason, true
	}
	for _, marker := range heavyBeside[name] {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), marker)); err == nil {
			return "build-output", true
		}
	}
	if _, err := os.Stat(filepath.Join(path, "pyvenv.cfg")); err == nil {
		return "dependencies", true
	}
	return "", false
}

// dirSize adds up the sizes of the files under dir, not following links.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// ignoredOf says which of found git already ignores in root, by the rules
// git add would use there: root's .gitignore files, .git/info/exclude when
// root is a repository, and the user's global excludes. A folder that isn't
// a repository yet is asked about through an empty one made for the
// question, so nothing is written to root.
func ignoredOf(root string, found []Skipped) (map[string]bool, error) {
	gitDir := filepath.Join(root, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		tmp, err := os.MkdirTemp("", "agentbox-ignore-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		if _, err := run(tmp, "init", "--quiet", "--bare", "."); err != nil {
			return nil, err
		}
		gitDir = tmp
	}
	var in strings.Builder
	for _, s := range found {
		in.WriteString(s.Path)
		in.WriteByte(0)
	}
	out, err := outputIn(root, in.String(), "--git-dir="+gitDir, "--work-tree=.", "check-ignore", "--stdin", "-z")
	// check-ignore exits 1 when nothing is ignored.
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil, err
	}
	ignored := map[string]bool{}
	for p := range strings.SplitSeq(out, "\x00") {
		if p != "" {
			ignored[strings.TrimSuffix(p, "/")] = true
		}
	}
	return ignored, nil
}

// exclude leaves skipped out of every commit in the repository at root, in
// .git/info/exclude rather than a .gitignore: the user's files stay as they
// were, and worktrees share it, so an agent's npm install isn't committed
// either.
func exclude(root string, skipped []Skipped) error {
	if len(skipped) == 0 {
		return nil
	}
	path := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("\n# Left out by AgentBox when it made this repository's first commit.\n# Delete a line, then git add, to commit it after all.\n")
	for _, s := range skipped {
		b.WriteString("/" + escapeIgnore(s.Path))
		if s.Reason != "large-file" {
			b.WriteString("/")
		}
		b.WriteString("\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(b.String())
	return errors.Join(err, f.Close())
}

// escapeIgnore quotes what gitignore would read as a pattern in a path.
func escapeIgnore(path string) string {
	var b strings.Builder
	for i, r := range path {
		if strings.ContainsRune(`*?[\`, r) || (i == 0 && (r == '#' || r == '!')) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

// outputIn is output with stdin.
func outputIn(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return "", &gitError{args: args, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return string(b), nil
}

func must[T any](v T, _ error) T { return v }
