package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"agentbox/internal/portable"
	"agentbox/internal/state"
)

// A repository goes in by reference: its remote URL and the branch it is on,
// which is all a clone needs. Asked for, it also goes in as a git bundle of
// every ref, which is what keeps branches that were never pushed (and a
// repository with no remote at all); uncommitted changes are never in it.

// RepositoryBundle is the kind of the entry holding a project's git bundle,
// beside its Repository entry.
const RepositoryBundle = "repository-bundle"

type repoRef struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	// Root is where it was, the first place an import looks for it.
	Root string `json:"root"`
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], firstLine(stderr.String(), err))
	}
	return strings.TrimSpace(string(out)), nil
}

func firstLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return err.Error()
	}
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// remoteOf is a repository's origin, or its first remote, or "".
func remoteOf(ctx context.Context, root string) string {
	if url, err := git(ctx, root, "remote", "get-url", "origin"); err == nil {
		return url
	}
	remotes, _ := git(ctx, root, "remote")
	if first, _, _ := strings.Cut(remotes, "\n"); first != "" {
		url, _ := git(ctx, root, "remote", "get-url", first)
		return url
	}
	return ""
}

func repoOf(ctx context.Context, root string) repoRef {
	r := repoRef{Root: root, Remote: remoteOf(ctx, root)}
	r.Branch, _ = git(ctx, root, "branch", "--show-current")
	r.Head, _ = git(ctx, root, "rev-parse", "HEAD")
	return r
}

// bundleEstimate is about how big a bundle of the repository would be: its
// objects, packed and loose.
func bundleEstimate(ctx context.Context, root string) int64 {
	out, err := git(ctx, root, "count-objects", "-v")
	if err != nil {
		return 0
	}
	var kib int64
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(line, ": ")
		if k == "size" || k == "size-pack" {
			n, _ := strconv.ParseInt(v, 10, 64)
			kib += n
		}
	}
	return kib * 1024
}

func (in Instance) exportRepository(ctx context.Context, w *portable.Writer, e portable.Entry, p state.Project, bundle bool) (portable.Entry, error) {
	ref := repoOf(ctx, p.Root)
	e.Items = 1
	e.Meta["remote"] = ref.Remote
	e.Meta["branch"] = ref.Branch
	e.Meta["root"] = ref.Root
	if bundle {
		be := portable.Entry{ID: ProjectEntryID(p.Name, RepositoryBundle), Kind: RepositoryBundle, Project: p.Name, Items: 1,
			Path: "projects/" + p.Name + "/repository.bundle"}
		if _, err := w.Add(be, "", func(out io.Writer) error {
			cmd := exec.CommandContext(ctx, "git", "bundle", "create", "--quiet", "-", "--all")
			cmd.Dir = p.Root
			var stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = out, &stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("git bundle: %s", firstLine(stderr.String(), err))
			}
			return nil
		}); err != nil {
			return e, err
		}
		e.Meta["bundled"] = "true"
	}
	return w.AddJSON(e, ref)
}

// Ways an import gets a project's repository.
const (
	RepoUse    = "use"    // a checkout already at the path
	RepoClone  = "clone"  // clone the remote to the path
	RepoBundle = "bundle" // restore the bundle to the path
)

// sameRemote compares remote URLs as places, so git@host:o/r.git and
// https://host/o/r are the same repository.
func sameRemote(a, b string) bool {
	return a != "" && normalizeRemote(a) == normalizeRemote(b)
}

func normalizeRemote(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		u = strings.TrimPrefix(u, p)
	}
	if at := strings.IndexByte(u, '@'); at >= 0 && at < strings.IndexAny(u+"/", ":/") {
		u = u[at+1:]
	}
	u = strings.Replace(u, ":", "/", 1)
	return strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isRepo(ctx context.Context, path string) bool {
	top, err := git(ctx, path, "rev-parse", "--show-toplevel")
	return err == nil && filepath.Clean(top) == filepath.Clean(path)
}

// suggestPath is where an import puts a project's repository unless told: where
// it was, when that is the same repository here or free to restore into, else
// ~/src/<project>.
func (in Instance) suggestPath(ctx context.Context, project string, ref repoRef) string {
	if ref.Root != "" {
		if exists(ref.Root) {
			if isRepo(ctx, ref.Root) && (ref.Remote == "" || sameRemote(remoteOf(ctx, ref.Root), ref.Remote)) {
				return ref.Root
			}
		} else if exists(filepath.Dir(ref.Root)) {
			return ref.Root
		}
	}
	return filepath.Join(in.Home, "src", project)
}

// planRepo says how a repository would get to path.
func planRepo(ctx context.Context, ref repoRef, bundled bool, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q isn't a full path", path)
	}
	if exists(path) {
		if isRepo(ctx, path) {
			return RepoUse, nil
		}
		if entries, err := os.ReadDir(path); err != nil || len(entries) > 0 {
			return "", fmt.Errorf("%s is there and isn't a git repository", path)
		}
	}
	switch {
	case bundled:
		// The bundle, even when there is a remote: it is everything the
		// remote has that was fetched, plus what was never pushed.
		return RepoBundle, nil
	case ref.Remote != "":
		return RepoClone, nil
	}
	return "", errors.New("the export has no remote URL or bundle for it: point it at a checkout here")
}

// restoreRepo puts the repository at path the way plan said.
func restoreRepo(ctx context.Context, plan, path string, ref repoRef, bundle string) error {
	switch plan {
	case RepoUse:
		return nil
	case RepoClone:
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if _, err := git(ctx, "", "clone", "--quiet", ref.Remote, path); err != nil {
			return err
		}
		if ref.Branch != "" {
			_, _ = git(ctx, path, "checkout", "--quiet", ref.Branch)
		}
		return nil
	case RepoBundle:
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		if _, err := git(ctx, path, "init", "--quiet"); err != nil {
			return err
		}
		if _, err := git(ctx, path, "fetch", "--quiet", "--update-head-ok", bundle, "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"); err != nil {
			return err
		}
		branch := ref.Branch
		if branch == "" {
			branch, _ = git(ctx, path, "for-each-ref", "--count=1", "--format=%(refname:short)", "refs/heads")
		}
		if branch != "" {
			if _, err := git(ctx, path, "checkout", "--quiet", "-f", branch); err != nil {
				return err
			}
		}
		if ref.Remote != "" {
			if _, err := git(ctx, path, "remote", "add", "origin", ref.Remote); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("no way %q to get a repository", plan)
}
