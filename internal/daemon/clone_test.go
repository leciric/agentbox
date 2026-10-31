package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// A project added from a link is cloned in a job, into the clone folder
// unless a folder is given, and what can refuse it is refused before the job
// starts: a link that isn't one, or a folder already there.
func TestCloneProjectClonesAndAddsInAJob(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	fixture := d.fixtureRepo(t, "hello-stack")
	var mu sync.Mutex
	var cloned []string
	fail := false
	d.srv.cloneHook = func(_ context.Context, repo, dest string) error {
		mu.Lock()
		defer mu.Unlock()
		cloned = append(cloned, repo+" "+dest)
		if fail {
			return errors.New("can't clone it: no access")
		}
		if out, err := exec.Command("git", "clone", "--quiet", fixture, dest).CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %w: %s", err, out)
		}
		return nil
	}
	d.srv.cloneRoot = filepath.Join(t.TempDir(), "src")

	settings, err := d.client.Settings(ctx)
	if err != nil || settings.CloneFolder != d.srv.cloneRoot {
		t.Fatalf("Settings().CloneFolder = %q, %v; want %s", settings.CloneFolder, err, d.srv.cloneRoot)
	}

	for _, bad := range []string{"/home/me/code/x", "https://github.com", "not a link", ""} {
		if _, err := d.client.CloneProject(ctx, api.CloneProjectRequest{URL: bad}); !api.HasReason(err, api.ErrorBadRepoURL) {
			t.Errorf("CloneProject(%q) = %v, want %s", bad, err, api.ErrorBadRepoURL)
		}
	}

	j, err := d.client.CloneProject(ctx, api.CloneProjectRequest{URL: "git@github.com:acme/hello-stack.git"})
	if err != nil {
		t.Fatal(err)
	}
	if j = waitJobDone(t, d, j.ID); j.Status != api.JobSucceeded || j.Kind != "clone" {
		t.Fatalf("job = %+v", j)
	}
	var p api.Project
	if err := json.Unmarshal(j.Result, &p); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(d.srv.cloneRoot, "hello-stack")
	if p.Name != "hello-stack" || p.Root != want {
		t.Errorf("cloned project = %s in %s, want hello-stack in %s", p.Name, p.Root, want)
	}

	// The same folder again is refused at once, with a way to add it instead.
	_, err = d.client.CloneProject(ctx, api.CloneProjectRequest{URL: "https://gitlab.com/acme/hello-stack", AddProjectRequest: api.AddProjectRequest{Path: want, Name: "again"}})
	if !api.HasReason(err, api.ErrorFolderExists) {
		t.Errorf("cloning into %s again = %v, want %s", want, err, api.ErrorFolderExists)
	}

	// A clone that fails fails the job, and adds nothing.
	mu.Lock()
	fail = true
	mu.Unlock()
	j, err = d.client.CloneProject(ctx, api.CloneProjectRequest{URL: "https://github.com/acme/secret"})
	if err != nil {
		t.Fatal(err)
	}
	if j = waitJobDone(t, d, j.ID); j.Status != api.JobFailed || !strings.Contains(j.Error, "no access") {
		t.Errorf("failed clone's job = %+v", j)
	}
	if _, err := d.client.Project(ctx, "secret"); err == nil {
		t.Error("a failed clone added a project")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cloned) != 2 || cloned[0] != "git@github.com:acme/hello-stack.git "+want {
		t.Errorf("cloned = %q", cloned)
	}
}

func TestCloneFolderIsASetting(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	rel := "code"
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{CloneFolder: &rel}); err == nil {
		t.Error("a relative clone folder was taken")
	}
	dir := filepath.Join(t.TempDir(), "code")
	settings, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{CloneFolder: &dir})
	if err != nil || settings.CloneFolder != dir {
		t.Fatalf("UpdateSettings(cloneFolder) = %q, %v", settings.CloneFolder, err)
	}
	empty := ""
	settings, err = d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{CloneFolder: &empty})
	home, _ := os.UserHomeDir()
	if err != nil || settings.CloneFolder != filepath.Join(home, "src") {
		t.Errorf("cloneFolder back to its default = %q, %v", settings.CloneFolder, err)
	}
}

func TestRepoName(t *testing.T) {
	t.Parallel()
	for u, want := range map[string]string{
		"https://github.com/acme/hello-stack": "hello-stack", "https://github.com/acme/hello-stack.git/": "hello-stack",
		"git@github.com:acme/x.git": "x", "git@host:x.git": "x", "ssh://git@host:22/a/b/c": "c",
	} {
		if got := repoName(u); got != want {
			t.Errorf("repoName(%q) = %q, want %q", u, got, want)
		}
	}
}
