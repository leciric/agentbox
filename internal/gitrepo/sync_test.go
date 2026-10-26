package gitrepo_test

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
)

// staleClone is a project cloned from a remote that has since moved on by a
// commit it hasn't fetched: the local main the daemon found at 0.6.0 while
// origin/main was at 0.7.0. It answers the clone and the remote's new tip.
func staleClone(t *testing.T) (gitrepo.Repo, string) {
	t.Helper()
	fixture := testutil.FixtureRepo(t, "hello-stack")
	remote := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, fixture, "clone", "--quiet", "--bare", fixture, remote)
	root := filepath.Join(t.TempDir(), "project")
	testutil.Git(t, fixture, "clone", "--quiet", remote, root)

	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, fixture, "clone", "--quiet", remote, other)
	write(t, other, "merged.txt", "a merged pull request")
	testutil.Git(t, other, "add", "merged.txt")
	testutil.Git(t, other, "commit", "--quiet", "-m", "Merged (#86)")
	testutil.Git(t, other, "push", "--quiet", "origin", "main")

	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return repo, testutil.Git(t, other, "rev-parse", "HEAD")
}

func TestBaseBranchAndUpstream(t *testing.T) {
	repo, _ := staleClone(t)
	branch, up, ok := repo.BaseBranch()
	if !ok || branch != "main" || up != (gitrepo.Upstream{Remote: "origin", Ref: "refs/remotes/origin/main"}) {
		t.Errorf("BaseBranch() = %q, %+v, %v; want main, origin's main", branch, up, ok)
	}

	// A branch that tracks nothing still has origin's copy of the same name.
	testutil.Git(t, repo.Root, "branch", "--quiet", "--no-track", "loose", "main")
	if up, ok := repo.UpstreamOf("loose"); !ok || up.Ref != "refs/remotes/origin/loose" {
		t.Errorf("UpstreamOf(loose) = %+v, %v; want origin's loose", up, ok)
	}
	// One that tracks a local branch has none.
	testutil.Git(t, repo.Root, "branch", "--quiet", "--track", "local-tracking", "main")
	if up, ok := repo.UpstreamOf("local-tracking"); ok {
		t.Errorf("UpstreamOf(local-tracking) = %+v, want none", up)
	}

	// A repository with no remote has no base branch to keep up to date.
	lonely, _ := gitrepo.Open(testutil.FixtureRepo(t, "hello-stack"))
	if branch, _, ok := lonely.BaseBranch(); ok {
		t.Errorf("BaseBranch() without a remote = %q", branch)
	}
}

// A fetch shows where the remote is; a fast-forward brings the checked-out
// main there, files and all, when nothing in the checkout is changed.
func TestFetchAndFastForwardCheckedOut(t *testing.T) {
	repo, newTip := staleClone(t)
	_, up, _ := repo.BaseBranch()
	if _, ok := repo.Ahead("main", up); ok {
		t.Fatal("Ahead() before a fetch: origin/main moved without one")
	}
	if err := repo.Fetch(context.Background(), up.Remote, ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := repo.Ahead("main", up); !ok || got != newTip {
		t.Fatalf("Ahead() after a fetch = %q, %v; want %s", got, ok, newTip)
	}

	// Changes to tracked files keep it where it is.
	write(t, repo.Root, "README.md", "the user's edit")
	if moved, err := repo.FastForward("main", up); moved || err != nil {
		t.Fatalf("FastForward() with the checkout changed = %v, %v; want it left alone", moved, err)
	}
	testutil.Git(t, repo.Root, "checkout", "--quiet", "--", "README.md")

	// An untracked file is no change to what's tracked.
	write(t, repo.Root, "scratch.txt", "untracked")
	if moved, err := repo.FastForward("main", up); !moved || err != nil {
		t.Fatalf("FastForward() = %v, %v; want it moved", moved, err)
	}
	if got, _ := repo.ResolveCommit("main"); got != newTip {
		t.Errorf("main = %s after a fast-forward, want %s", got, newTip)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "merged.txt")); err != nil {
		t.Errorf("the checkout's files didn't move with main: %v", err)
	}
	if out := testutil.Git(t, repo.Root, "status", "--porcelain", "--untracked-files=no"); out != "" {
		t.Errorf("the checkout isn't clean after a fast-forward:\n%s", out)
	}
	if moved, err := repo.FastForward("main", up); moved || err != nil {
		t.Errorf("FastForward() again = %v, %v; want nothing to do", moved, err)
	}
}

// A base branch checked out nowhere moves in place; one with commits of its
// own never moves, and neither does one that is ahead.
func TestFastForwardNotCheckedOutAndDiverged(t *testing.T) {
	repo, newTip := staleClone(t)
	_, up, _ := repo.BaseBranch()
	if err := repo.Fetch(context.Background(), up.Remote, ""); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Root, "checkout", "--quiet", "-b", "feature")
	if moved, err := repo.FastForward("main", up); !moved || err != nil {
		t.Fatalf("FastForward() of a main checked out nowhere = %v, %v", moved, err)
	}
	if got, _ := repo.ResolveCommit("main"); got != newTip {
		t.Errorf("main = %s, want %s", got, newTip)
	}

	// Diverged: main has a commit origin/main doesn't.
	testutil.Git(t, repo.Root, "branch", "--quiet", "-f", "main", "main~1")
	testutil.Git(t, repo.Root, "checkout", "--quiet", "main")
	write(t, repo.Root, "mine.txt", "unpushed")
	testutil.Git(t, repo.Root, "add", "mine.txt")
	testutil.Git(t, repo.Root, "commit", "--quiet", "-m", "unpushed")
	mine, _ := repo.ResolveCommit("main")
	if _, ok := repo.Ahead("main", up); ok {
		t.Error("Ahead() of a diverged main")
	}
	if moved, err := repo.FastForward("main", up); moved || err != nil {
		t.Errorf("FastForward() of a diverged main = %v, %v", moved, err)
	}
	if got, _ := repo.ResolveCommit("main"); got != mine {
		t.Errorf("a diverged main moved to %s", got)
	}

	// Ahead: origin/main is an ancestor of main.
	testutil.Git(t, repo.Root, "reset", "--quiet", "--hard", newTip)
	write(t, repo.Root, "ahead.txt", "ahead")
	testutil.Git(t, repo.Root, "add", "ahead.txt")
	testutil.Git(t, repo.Root, "commit", "--quiet", "-m", "ahead")
	if moved, _ := repo.FastForward("main", up); moved {
		t.Error("FastForward() moved a main that was ahead")
	}
}

func TestFetchFailsWithoutWaiting(t *testing.T) {
	repo, _ := staleClone(t)
	testutil.Git(t, repo.Root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if err := repo.Fetch(context.Background(), "origin", ""); err == nil {
		t.Error("Fetch() from a remote that isn't there succeeded")
	}
}

// A fetch with a GitHub token authenticates with it, whatever credential
// helper the user's gitconfig names: in AgentBox's VM, the host's
// `gh auth setup-git` points git at a gh that isn't there, and every
// private project's base went stale.
func TestFetchWithGitHubTokenIgnoresTheUsersHelper(t *testing.T) {
	repo, newTip := staleClone(t)
	_, up, _ := repo.BaseBranch()
	remote := testutil.Git(t, repo.Root, "config", "remote.origin.url")

	const token = "the-projects-token"
	backend, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "x-access-token" || pass != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="github"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		(&cgi.Handler{
			Path: backend,
			Args: []string{"http-backend"},
			Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remote), "GIT_HTTP_EXPORT_ALL=1"},
		}).ServeHTTP(w, r)
	}))
	defer srv.Close()
	gitrepo.SetGitHubURL(t, srv.URL)
	testutil.Git(t, repo.Root, "remote", "set-url", "origin", srv.URL+"/"+filepath.Base(remote))

	// What `gh auth setup-git` writes, with a gh that doesn't exist.
	missing := "!" + filepath.Join(t.TempDir(), "gh") + " auth git-credential"
	config := filepath.Join(t.TempDir(), "gitconfig")
	write(t, filepath.Dir(config), filepath.Base(config), "[credential]\n\thelper = "+missing+"\n"+
		"[credential \""+srv.URL+"\"]\n\thelper =\n\thelper = "+missing+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)

	if err := repo.Fetch(context.Background(), up.Remote, ""); err == nil {
		t.Fatal("Fetch() without a token got past the server, so this test proves nothing")
	}
	if err := repo.Fetch(context.Background(), up.Remote, "a-wrong-token"); err == nil {
		t.Error("Fetch() with the wrong token succeeded")
	}
	if err := repo.Fetch(context.Background(), up.Remote, token); err != nil {
		t.Fatalf("Fetch() with the project's token: %v", err)
	}
	if got, ok := repo.Ahead("main", up); !ok || got != newTip {
		t.Errorf("Ahead() after a fetch = %q, %v; want %s", got, ok, newTip)
	}
	if b, err := os.ReadFile(config); err != nil || !strings.Contains(string(b), missing) || strings.Contains(string(b), token) {
		t.Errorf("the user's gitconfig changed: %q, %v", b, err)
	}
}
