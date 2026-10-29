package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func addGitProject(t *testing.T, st *Store, name, display string) {
	t.Helper()
	if err := st.AddProject(context.Background(), Project{Name: name, DisplayName: display, Root: "/src/" + name, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func gitRemote(t *testing.T, st *Store, project, profile string) (GitRemote, bool) {
	t.Helper()
	remotes, err := st.ProjectGitRemotes(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range remotes {
		if r.Profile == profile {
			return r, true
		}
	}
	return GitRemote{}, false
}

func TestGitRemoteRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	addGitProject(t, st, "pawly", "Pawly App")

	// A fresh project has neither profile, and reads as the private one.
	if remotes, err := st.ProjectGitRemotes(ctx, "pawly"); err != nil || len(remotes) != 0 {
		t.Fatalf("ProjectGitRemotes of a new project = %v, %v", remotes, err)
	}
	if p, err := st.Project(ctx, "pawly"); err != nil || p.AgentRemote != GitProfilePrivate {
		t.Fatalf("a new project's agent remote = %q, %v", p.AgentRemote, err)
	}

	private := GitRemote{Project: "pawly", Profile: GitProfilePrivate, URL: "forge:pawly/app.git", Auth: GitAuthToken, Credential: "forgejo"}
	public := GitRemote{Project: "Pawly App", Profile: GitProfilePublic, URL: "git@github.com:pawly/app.git", Auth: GitAuthGitHubAccount, Credential: "work"}
	// Set out of profile order, to prove the answer is ordered and not just
	// what the rows came back in.
	if err := st.SetProjectGitRemote(ctx, public); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectGitRemote(ctx, private); err != nil {
		t.Fatal(err)
	}

	remotes, err := st.ProjectGitRemotes(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 2 || remotes[0].Profile != GitProfilePrivate || remotes[1].Profile != GitProfilePublic {
		t.Fatalf("ProjectGitRemotes = %+v, want private then public", remotes)
	}
	if remotes[0] != private {
		t.Errorf("private profile = %+v, want %+v", remotes[0], private)
	}
	// Addressed by what the project is called, it is stored against its slug.
	if remotes[1].Project != "pawly" {
		t.Errorf("public profile's project = %q, want the slug pawly", remotes[1].Project)
	}
	if remotes[1].URL != public.URL || remotes[1].Auth != public.Auth || remotes[1].Credential != public.Credential {
		t.Errorf("public profile = %+v, want %+v", remotes[1], public)
	}

	// Setting a profile again replaces it rather than adding a third row.
	private.URL = "https://hub.example.com/pawly/app.git"
	private.Auth = GitAuthSSHKey
	private.Credential = "hub-key"
	if err := st.SetProjectGitRemote(ctx, private); err != nil {
		t.Fatal(err)
	}
	if remotes, err := st.ProjectGitRemotes(ctx, "pawly"); err != nil || len(remotes) != 2 {
		t.Fatalf("after replacing private, ProjectGitRemotes = %+v, %v", remotes, err)
	}
	if got, ok := gitRemote(t, st, "pawly", GitProfilePrivate); !ok || got != private {
		t.Errorf("private profile after replacing it = %+v, %v, want %+v", got, ok, private)
	}
}

func TestProjectAgentRemote(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	addGitProject(t, st, "pawly", "")

	// A profile the project doesn't have is refused, and says which one.
	err := st.SetProjectAgentRemote(ctx, "pawly", GitProfilePublic)
	if err == nil || !strings.Contains(err.Error(), "no public git profile") {
		t.Fatalf("pointing agents at a missing profile = %v, want it refused", err)
	}
	// Neither is a profile that isn't one.
	if err := st.SetProjectAgentRemote(ctx, "pawly", "mirror"); err == nil {
		t.Fatal("a made-up profile was accepted")
	}
	// An unknown project is not found rather than created.
	if err := st.SetProjectAgentRemote(ctx, "nope", GitProfilePrivate); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetProjectAgentRemote of an unknown project = %v, want ErrNotFound", err)
	}

	if err := st.SetProjectGitRemote(ctx, GitRemote{Project: "pawly", Profile: GitProfilePublic, URL: "https://github.com/pawly/app.git", Auth: GitAuthToken, Credential: "github"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectAgentRemote(ctx, "pawly", GitProfilePublic); err != nil {
		t.Fatal(err)
	}
	if p, err := st.Project(ctx, "pawly"); err != nil || p.AgentRemote != GitProfilePublic {
		t.Fatalf("after use public, agent remote = %q, %v", p.AgentRemote, err)
	}
	projects, err := st.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].AgentRemote != GitProfilePublic {
		t.Fatalf("Projects() = %+v, want the public profile", projects)
	}

	// A row that says nothing, which is what one written before the column
	// existed looks like, is private.
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET agent_remote = '' WHERE name = 'pawly'`); err != nil {
		t.Fatal(err)
	}
	if p, err := st.Project(ctx, "pawly"); err != nil || p.AgentRemote != GitProfilePrivate {
		t.Fatalf("an empty agent remote = %q, %v, want private", p.AgentRemote, err)
	}
}

func TestRemoveProjectGitRemote(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	addGitProject(t, st, "pawly", "")
	for _, r := range []GitRemote{
		{Project: "pawly", Profile: GitProfilePrivate, URL: "https://hub.example.com/pawly/app.git", Auth: GitAuthToken, Credential: "forgejo"},
		{Project: "pawly", Profile: GitProfilePublic, URL: "https://github.com/pawly/app.git", Auth: GitAuthToken, Credential: "github"},
	} {
		if err := st.SetProjectGitRemote(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetProjectAgentRemote(ctx, "pawly", GitProfilePublic); err != nil {
		t.Fatal(err)
	}

	// Removing the profile nobody uses leaves the agents where they are.
	if err := st.RemoveProjectGitRemote(ctx, "pawly", GitProfilePrivate); err != nil {
		t.Fatal(err)
	}
	if _, ok := gitRemote(t, st, "pawly", GitProfilePrivate); ok {
		t.Error("the private profile is still there")
	}
	if p, err := st.Project(ctx, "pawly"); err != nil || p.AgentRemote != GitProfilePublic {
		t.Fatalf("after removing the unused profile, agent remote = %q, %v", p.AgentRemote, err)
	}
	if err := st.RemoveProjectGitRemote(ctx, "pawly", GitProfilePrivate); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing a profile twice = %v, want ErrNotFound", err)
	}

	// Removing the profile the agents use hands them to the other one.
	if err := st.SetProjectGitRemote(ctx, GitRemote{Project: "pawly", Profile: GitProfilePrivate, URL: "https://hub.example.com/pawly/app.git", Auth: GitAuthToken, Credential: "forgejo"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectAgentRemote(ctx, "pawly", GitProfilePrivate); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveProjectGitRemote(ctx, "pawly", GitProfilePrivate); err != nil {
		t.Fatal(err)
	}
	if p, err := st.Project(ctx, "pawly"); err != nil || p.AgentRemote != GitProfilePublic {
		t.Fatalf("after removing the profile in use, agent remote = %q, %v", p.AgentRemote, err)
	}

	// And the last one leaves nothing to point at, so it reads as the default.
	if err := st.RemoveProjectGitRemote(ctx, "pawly", GitProfilePublic); err != nil {
		t.Fatal(err)
	}
	if p, err := st.Project(ctx, "pawly"); err != nil || p.AgentRemote != GitProfilePrivate {
		t.Fatalf("after removing every profile, agent remote = %q, %v", p.AgentRemote, err)
	}
	if remotes, err := st.ProjectGitRemotes(ctx, "pawly"); err != nil || len(remotes) != 0 {
		t.Fatalf("ProjectGitRemotes after removing every profile = %v, %v", remotes, err)
	}
}

func TestGitRemoteValidation(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	addGitProject(t, st, "pawly", "")

	// A project that isn't there is not found rather than stored.
	if err := st.SetProjectGitRemote(ctx, GitRemote{Project: "nope", Profile: GitProfilePrivate, URL: "https://hub.example.com/a/b.git", Auth: GitAuthToken}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a remote of an unknown project = %v, want ErrNotFound", err)
	}

	good := GitRemote{Project: "pawly", Profile: GitProfilePrivate, URL: "https://hub.example.com/a/b.git", Auth: GitAuthToken}
	for _, tc := range []struct {
		name string
		edit func(*GitRemote)
	}{
		{"profile", func(r *GitRemote) { r.Profile = "mirror" }},
		{"auth", func(r *GitRemote) { r.Auth = "password" }},
		{"github account without a credential", func(r *GitRemote) { r.Auth = GitAuthGitHubAccount }},
		{"auth token without a credential", func(r *GitRemote) {
			r.URL, r.Auth, r.Credential = "", GitAuthToken, ""
		}},
		{"a relative URL", func(r *GitRemote) { r.URL = "./b.git" }},
		{"a URL with no host", func(r *GitRemote) { r.URL = "https:///a/b.git" }},
		{"an alias with nothing after it", func(r *GitRemote) { r.URL = "forge:" }},
		{"a credential with a space", func(r *GitRemote) { r.Credential = "two words" }},
		{"a password in the URL", func(r *GitRemote) { r.URL = "https://user:secret@hub.example.com/a/b.git" }},
		// git's ls-remote and clone would take these as options: the first
		// runs its command as upload-pack.
		{"an option as the URL", func(r *GitRemote) { r.URL = "--upload-pack=touch${IFS}pwned@h:p" }},
		{"an option as the shorthand's host", func(r *GitRemote) { r.URL = "git@-oProxyCommand=touch${IFS}pwned:p" }},
		{"an option as the URL's user", func(r *GitRemote) { r.URL = "ssh://-oProxyCommand=x@hub.example.com/a/b.git" }},
		{"an option as the URL's host", func(r *GitRemote) { r.URL = "ssh://-oProxyCommand=x/a/b.git" }},
		{"a token over plain http", func(r *GitRemote) {
			r.URL, r.Credential = "http://host.k3d.internal:3000/a/b.git", "forgejo"
		}},
		{"a GitHub account over plain http", func(r *GitRemote) {
			r.URL, r.Auth, r.Credential = "http://github.com/a/b.git", GitAuthGitHubAccount, "me"
		}},
		{"a GitHub account off GitHub", func(r *GitRemote) {
			r.URL, r.Auth, r.Credential = "https://gitlab.com/a/b.git", GitAuthGitHubAccount, "me"
		}},
		{"a GitHub account in shorthand off GitHub", func(r *GitRemote) {
			r.URL, r.Auth, r.Credential = "git@gitlab.com:a/b.git", GitAuthGitHubAccount, "me"
		}},
	} {
		r := good
		tc.edit(&r)
		err := st.SetProjectGitRemote(ctx, r)
		if err == nil {
			t.Errorf("%s was accepted: %+v", tc.name, r)
			continue
		}
		if strings.Contains(err.Error(), "secret") && !strings.Contains(err.Error(), "***") {
			t.Errorf("%s: the error repeats the password: %v", tc.name, err)
		}
	}

	for _, url := range []string{
		"https://hub.example.com/a/b.git",
		"http://host.k3d.internal:3000/a/b.git",
		"ssh://git@hub.example.com:2222/a/b.git",
		"git://hub.example.com/a/b.git",
		"git@hub.example.com:a/b.git",
		"forge:h4ad/agentbox.git",
	} {
		if err := st.SetProjectGitRemote(ctx, GitRemote{Project: "pawly", Profile: GitProfilePrivate, URL: url, Auth: GitAuthToken}); err != nil {
			t.Errorf("%s was refused: %v", url, err)
		}
	}
}

func TestGitHostname(t *testing.T) {
	for raw, want := range map[string]string{
		"https://GitHub.com/o/r.git":       "github.com",
		"https://github.com:443/o/r.git":   "github.com",
		"ssh://git@github.com/o/r.git":     "github.com",
		"git@github.com:o/r.git":           "github.com",
		"github.com:o/r.git":               "github.com",
		"https://github.com.evil.test/o/r": "github.com.evil.test",
		"https://user@gitlab.com/o/r.git":  "gitlab.com",
		"forge:o/r.git":                    "",
		"./o/r.git":                        "",
		"/srv/git/o/r.git":                 "",
	} {
		if got := GitHostname(raw); got != want {
			t.Errorf("GitHostname(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A project added again under the same name starts with no remotes of its own,
// never with the old ones.
func TestRemoveProjectTakesItsGitRemotes(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	addGitProject(t, st, "pawly", "")
	addGitProject(t, st, "other", "")
	for _, project := range []string{"pawly", "other"} {
		if err := st.SetProjectGitRemote(ctx, GitRemote{Project: project, Profile: GitProfilePrivate,
			URL: "https://hub.example.com/" + project + "/app.git", Auth: GitAuthToken, Credential: "forgejo"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, "project_git_remotes", "pawly"); n != 0 {
		t.Errorf("the removed project still has %d git remote(s)", n)
	}
	if n := countRows(t, st, "project_git_remotes", "other"); n != 1 {
		t.Errorf("the other project has %d git remote(s), want its own", n)
	}
	addGitProject(t, st, "pawly", "")
	if remotes, err := st.ProjectGitRemotes(ctx, "pawly"); err != nil || len(remotes) != 0 {
		t.Fatalf("a project added again has %v, %v", remotes, err)
	}
}
