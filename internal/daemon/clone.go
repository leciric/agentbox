package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A project added from a link is a repository cloned and then added like any
// folder. The app and the CLI do it as a job (POST /v1/projects/clone), the
// Home chat's add_project and another machine's lead (peers.go) as part of
// their own request; all of them go through cloneAndAdd, which clones with
// agent.Manager.Clone.

// badRepoURL is cloneable refusing what isn't a repository's link.
type badRepoURL string

func (u badRepoURL) Error() string {
	return fmt.Sprintf("%q isn't a repository's link: give one like https://github.com/owner/repo or git@github.com:owner/repo", string(u))
}

// cloneable refuses what can't be a repository's link on a git host: a path,
// on this machine or another (/home/…, file://, C:\…), or a host with no
// repository after it.
func cloneable(u string) error {
	ok := false
	if i := strings.Index(u, "://"); i > 0 {
		switch strings.ToLower(u[:i]) {
		case "https", "http", "ssh", "git":
			ok = true
		}
	} else if host, _, found := strings.Cut(u, ":"); found && len(host) > 1 && !strings.ContainsAny(host, `/\`) {
		// scp-like, git@github.com:owner/repo, and not a Windows drive (C:)
		ok = true
	}
	if ok && !strings.ContainsAny(u, " \t\n") {
		if _, rest, _ := strings.Cut(repoKey(u), "/"); rest != "" {
			return nil
		}
	}
	return badRepoURL(u)
}

// repoName is what a project cloned from a repository is called unless it is
// given a name: the repository's, without .git.
func repoName(u string) string {
	u = strings.TrimRight(u, "/")
	u = u[strings.LastIndexAny(u, "/:")+1:]
	return strings.TrimSuffix(u, ".git")
}

// cloneProject answers POST /v1/projects/clone. What can refuse the project
// (the link, the name, the accounts, a folder already there) is checked
// before the job starts, so the answer says so at once.
func (s *Server) cloneProject(w http.ResponseWriter, r *http.Request) error {
	var req api.CloneProjectRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	url := strings.TrimSpace(req.URL)
	if err := cloneable(url); err != nil {
		return err
	}
	add, err := s.cloneRequest(r.Context(), url, req.AddProjectRequest)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(add.Path); err == nil {
		return fmt.Errorf("%s already exists: add it as it is, or clone into another folder: %w", add.Path, agent.ErrCloneDestExists)
	}
	return s.startJob(w, "clone", add.Name, func(ctx context.Context, log io.Writer) (any, error) {
		p, err := s.cloneAndAdd(ctx, url, add, log)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(log, "==> Added the project %s from %s\n", p.DisplayName, p.Root)
		return s.projectInfo(ctx, p), nil
	})
}

// cloneRequest checks a project to be cloned from url and fills in what it
// left out: its name, the repository's, and its folder, in the clone folder.
// The link is the caller's to check: the Home chat may clone a repository on
// this machine, the app and another machine's lead may not (cloneable).
func (s *Server) cloneRequest(ctx context.Context, url string, add api.AddProjectRequest) (api.AddProjectRequest, error) {
	add.Create, add.CommitFiles, add.CopyToLinux = false, false, false
	add.Name = strings.TrimSpace(add.Name)
	if add.Name == "" {
		add.Name = repoName(url)
	}
	if err := s.projectNameFree(ctx, add.Name); err != nil {
		return add, err
	}
	if err := s.checkClaudeAccount(strings.TrimSpace(add.ClaudeAccount)); err != nil {
		return add, err
	}
	if err := s.checkGitHubAccount(strings.TrimSpace(add.GitHubAccount)); err != nil {
		return add, err
	}
	if _, err := s.newProjectToolAccounts(add); err != nil {
		return add, err
	}
	if strings.TrimSpace(add.Path) != "" {
		p, err := fullPath(add.Path)
		if err != nil {
			return add, err
		}
		add.Path = p
		return add, nil
	}
	root, err := s.cloneFolder(ctx)
	if err != nil {
		return add, err
	}
	slug, err := s.store.ProjectSlug(ctx, add.Name)
	if err != nil {
		return add, err
	}
	add.Path = filepath.Join(root, slug)
	return add, nil
}

// cloneAndAdd clones url into add.Path and adds it, and takes the clone away
// again if it can't be added, so asking twice doesn't find the folder in the
// way.
func (s *Server) cloneAndAdd(ctx context.Context, url string, add api.AddProjectRequest, log io.Writer) (state.Project, error) {
	if err := s.cloneRepo(ctx, url, add.Path, strings.TrimSpace(add.GitHubAccount), log); err != nil {
		return state.Project{}, err
	}
	p, err := s.addProjectRepo(ctx, add)
	if err != nil {
		_ = os.RemoveAll(add.Path)
		return state.Project{}, err
	}
	s.logf("cloned %s into %s as the project %s", url, add.Path, p.Name)
	return p, nil
}

// cloneRepo clones a repository with the GitHub account githubAccount ("" for
// the machine's default); tests replace it with cloneHook.
func (s *Server) cloneRepo(ctx context.Context, repo, dest, githubAccount string, log io.Writer) error {
	if s.cloneHook != nil {
		return s.cloneHook(ctx, repo, dest)
	}
	return s.manager(s.cfg.Log).Clone(ctx, repo, dest, githubAccount, log)
}

// cloneFolder is where a project added from a link is cloned when no folder
// was chosen for it: Settings.CloneFolder, or ~/src.
func (s *Server) cloneFolder(ctx context.Context) (string, error) {
	if dir, err := s.store.Setting(ctx, state.SettingCloneFolder); err != nil || dir != "" {
		return dir, err
	}
	if s.cloneRoot != "" {
		return s.cloneRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "src"), nil
}

// fullPath is a folder given in a request as a full path, ~/ standing for the
// home folder.
func fullPath(p string) (string, error) {
	given := p
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q isn't a full path: give the folder from /", given)
	}
	return filepath.Clean(p), nil
}

// projectNameOf is what a project cloned for another machine is called: the
// repository's name, or that with -2, -3… when it's taken here.
func (s *Server) projectNameOf(ctx context.Context, repo string) (string, error) {
	base := strings.TrimSuffix(path.Base(strings.TrimRight(repoKey(repo), "/")), ".git")
	name := base
	for n := 2; s.projectNameFree(ctx, name) != nil; n++ {
		if n > 50 {
			return "", fmt.Errorf("every name like %s is taken by another project here", base)
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name, nil
}
