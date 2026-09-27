package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/testutil"
)

// startsAt makes an agent, with a machine copy that fails once the branch is
// made and says where the branch starts, and answers that.
func startsAt(t *testing.T, f fixture, slug string) string {
	t.Helper()
	_, err := f.m.Create(context.Background(), "hello-stack", agent.CreateOptions{AI: "none", Branch: slug})
	if err == nil {
		t.Fatal("Create() succeeded: the fake copy should have failed")
	}
	_, at, ok := strings.Cut(err.Error(), "branch at ")
	if !ok {
		t.Fatalf("Create() error = %v, want where the branch was made", err)
	}
	return strings.Fields(at)[0]
}

const reportBranch = `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: branch at $(git -C "$ROOT" rev-parse --verify --quiet "refs/heads/agentbox/$SLUG")" >&2; exit 1 ;;
esac`

// withRemote gives the fixture's project a real remote, which main tracks,
// and moves the remote on by a commit the project hasn't fetched: the user's
// main at 0.6.0 while origin's is at 0.7.0. It answers the remote's new tip.
func withRemote(t *testing.T, f fixture) string {
	t.Helper()
	root := f.repo.Root
	remote := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, root, "clone", "--quiet", "--bare", root, remote)
	testutil.Git(t, root, "remote", "add", "origin", remote)
	testutil.Git(t, root, "fetch", "--quiet", "origin")
	testutil.Git(t, root, "branch", "--quiet", "--set-upstream-to=origin/main", "main")

	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, root, "clone", "--quiet", remote, other)
	testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-m", "Release 0.7.0 (#86)")
	testutil.Git(t, other, "push", "--quiet", "origin", "main")
	return testutil.Git(t, other, "rev-parse", "HEAD")
}

// With the project keeping main up to date, creating an agent fetches first
// and fast-forwards the user's clean main, and the agent starts there.
func TestCreateSyncsAStaleMainFirst(t *testing.T) {
	f := setup(t, fakeIncus(t, reportBranch))
	t.Setenv("ROOT", f.repo.Root)
	t.Setenv("SLUG", "synced")
	newTip := withRemote(t, f)

	if at := startsAt(t, f, "synced"); at != newTip {
		t.Errorf("the agent started at %s, want origin's main, %s", at, newTip)
	}
	if main, _ := f.repo.ResolveCommit("main"); main != newTip {
		t.Errorf("main = %s, want it fast-forwarded to %s", main, newTip)
	}
}

// When main can't be moved — here the setting is off, and main has changes in
// its checkout — the agent still starts from origin's main as last fetched,
// and the user's main stays exactly where it was.
func TestCreateStartsFromTheRemoteWhenMainIsBehind(t *testing.T) {
	f := setup(t, fakeIncus(t, reportBranch))
	t.Setenv("ROOT", f.repo.Root)
	ctx := context.Background()
	newTip := withRemote(t, f)
	stale, _ := f.repo.ResolveCommit("main")
	testutil.Git(t, f.repo.Root, "fetch", "--quiet", "origin")

	if err := f.st.SetProjectBaseSync(ctx, "hello-stack", false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLUG", "behind")
	if at := startsAt(t, f, "behind"); at != newTip {
		t.Errorf("the agent started at %s, want origin's main, %s", at, newTip)
	}
	if main, _ := f.repo.ResolveCommit("main"); main != stale {
		t.Errorf("main moved to %s with the setting off", main)
	}

	// On, but main's checkout has changes: it isn't moved either.
	if err := f.st.SetProjectBaseSync(ctx, "hello-stack", true); err != nil {
		t.Fatal(err)
	}
	first := strings.Fields(testutil.Git(t, f.repo.Root, "ls-files"))[0]
	if err := os.WriteFile(filepath.Join(f.repo.Root, first), []byte("the user's edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLUG", "dirty")
	if at := startsAt(t, f, "dirty"); at != newTip {
		t.Errorf("the agent started at %s, want origin's main, %s", at, newTip)
	}
	if main, _ := f.repo.ResolveCommit("main"); main != stale {
		t.Errorf("main moved to %s with changes in its checkout", main)
	}

	// Asked to start from somewhere else, it starts there.
	testutil.Git(t, f.repo.Root, "branch", "--quiet", "other", stale)
	t.Setenv("SLUG", "from-other")
	_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none", Branch: "from-other", From: "other"})
	if err == nil || !strings.Contains(err.Error(), "branch at "+stale) {
		t.Errorf("Create(From: other) = %v, want it started at %s", err, stale)
	}
}

// A main with commits of its own is never the remote's to replace.
func TestCreateKeepsADivergedMain(t *testing.T) {
	f := setup(t, fakeIncus(t, reportBranch))
	t.Setenv("ROOT", f.repo.Root)
	t.Setenv("SLUG", "diverged")
	withRemote(t, f)
	testutil.Git(t, f.repo.Root, "commit", "--quiet", "--allow-empty", "-m", "unpushed")
	mine, _ := f.repo.ResolveCommit("main")

	if at := startsAt(t, f, "diverged"); at != mine {
		t.Errorf("the agent started at %s, want the user's own main, %s", at, mine)
	}
	if main, _ := f.repo.ResolveCommit("main"); main != mine {
		t.Errorf("a diverged main moved to %s", main)
	}
}
