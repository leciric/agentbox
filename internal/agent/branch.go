package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"agentbox/internal/gitrepo"
	"agentbox/internal/naming"
	"agentbox/internal/state"
)

// MaxBranchSlugLen caps what comes after the project's prefix in an agent's
// branch: long enough for "fix-context-budget-warning-after-consolidation",
// short enough to read in the rail and in a pull request list.
const MaxBranchSlugLen = 48

var branchSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// CheckBranchSlug reports whether slug can follow a project's prefix in an
// agent's branch: lowercase kebab-case, which is always a valid git ref, at
// most MaxBranchSlugLen characters. The empty slug is allowed and means
// "name it after the work".
func CheckBranchSlug(slug string) error {
	if slug == "" {
		return nil
	}
	if len(slug) > MaxBranchSlugLen || !branchSlug.MatchString(slug) {
		return fmt.Errorf("invalid branch %q: use lowercase letters, digits and single hyphens, like fix-login-redirect, at most %d characters", slug, MaxBranchSlugLen)
	}
	return nil
}

// SlugForBranch turns a title or a task into a branch slug: lowercase
// kebab-case, cut at a word boundary to fit MaxBranchSlugLen. It returns ""
// when nothing in s is usable.
func SlugForBranch(s string) string {
	// Only the first line: a task's first line says what it is about, and
	// the rest is detail no branch name should carry.
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	slug := naming.Slug(s)
	if len(slug) <= MaxBranchSlugLen {
		return slug
	}
	slug = slug[:MaxBranchSlugLen+1]
	if i := strings.LastIndex(slug, "-"); i > 0 {
		return slug[:i]
	}
	return strings.TrimRight(slug[:MaxBranchSlugLen], "-")
}

// branchFor is the branch a new agent is created on: the project's prefix,
// then the slug asked for, or else one made from its title, then its task,
// then its own name. A slug that is already a branch — here, on a remote as
// last fetched, or any agent's of this project — gets -2, -3… appended, so an
// agent never commits onto someone else's work.
func (m *Manager) branchFor(ctx context.Context, p state.Project, repo gitrepo.Repo, requested, title, task, name string) string {
	slug := requested
	for _, s := range []string{title, task} {
		if slug == "" {
			slug = SlugForBranch(s)
		}
	}
	if slug == "" {
		slug = name
	}
	taken := map[string]bool{}
	if agents, err := m.Store.Agents(ctx, p.Name); err == nil {
		for _, a := range agents {
			taken[a.Branch] = true
		}
	}
	// Every candidate starts with prefix+slug, so one listing covers them all.
	if branches, err := repo.BranchesStartingWith(p.BranchPrefix + slug); err == nil {
		for _, b := range branches {
			taken[b] = true
		}
	}
	return uniqueBranch(p.BranchPrefix, slug, func(b string) bool { return taken[b] || repo.BranchExists(b) })
}

// uniqueBranch is prefix+slug, or the first of prefix+slug-2, -3… that isn't
// taken. The suffix is fitted inside MaxBranchSlugLen by shortening the slug.
func uniqueBranch(prefix, slug string, taken func(string) bool) string {
	if !taken(prefix + slug) {
		return prefix + slug
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		base := slug
		if len(base)+len(suffix) > MaxBranchSlugLen {
			base = strings.TrimRight(base[:MaxBranchSlugLen-len(suffix)], "-")
		}
		if b := prefix + base + suffix; !taken(b) {
			return b
		}
	}
}
