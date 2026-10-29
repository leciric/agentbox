package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// gitRequest calls one of the API's routes and answers what it wrote.
func gitRequest(t *testing.T, srv *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	srv.routes().ServeHTTP(rec, r)
	return rec
}

// gitResponse decodes a route's answer, failing on anything but a 200.
func gitResponse(t *testing.T, rec *httptest.ResponseRecorder) api.ProjectGit {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out api.ProjectGit
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v", rec.Body.String(), err)
	}
	return out
}

// gitTestServer is a daemon that is never run, with one project whose
// repository is a temp directory: the git profile routes are all it is for.
func gitTestServer(t *testing.T) (*Server, state.Project) {
	t.Helper()
	srv := newTestServer(t, "")
	p, err := srv.registerProject(context.Background(), state.Project{
		Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, p
}

// gitFakeRemote is an HTTPS git remote that challenges for a login and records
// the one it was given: enough to prove the profile's credential is what git
// sends, without a real git server.
type gitFakeRemote struct {
	server *httptest.Server
	mu     sync.Mutex
	auth   string
}

func newGitFakeRemote(t *testing.T) *gitFakeRemote {
	t.Helper()
	// https, because a token is never sent over plain http; git takes the
	// test server's own certificate.
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	f := &gitFakeRemote{}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			f.mu.Lock()
			f.auth = auth
			f.mu.Unlock()
			// An empty body is an empty remote, which ls-remote accepts.
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="agentbox-test"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *gitFakeRemote) url() string { return f.server.URL + "/pawly.git" }

func (f *gitFakeRemote) login() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth
}

// TestProjectGitSetUseTestRemove is the whole route set: a profile and its
// login written by one request, which one the agents use, that the login is
// what the remote receives, and that removing a profile takes its login with
// it when no other profile names it.
func TestProjectGitSetUseTestRemove(t *testing.T) {
	ctx := context.Background()
	srv, p := gitTestServer(t)
	remote := newGitFakeRemote(t)
	creds := srv.gitCreds()
	if err := creds.SaveGitHubToken("me", "gh-token"); err != nil {
		t.Fatal(err)
	}

	// Private, with the token delivered in the same request.
	body := `{"url":"` + remote.url() + `","auth":"token","credential":"private","secret":"the-token"}`
	pg := gitResponse(t, gitRequest(t, srv, http.MethodPut, "/v1/projects/"+p.Name+"/git/private", body))
	if pg.AgentRemote != state.GitProfilePrivate || len(pg.Profiles) != 1 {
		t.Fatalf("after set: %+v", pg)
	}
	profile := pg.Profiles[0]
	if profile.URL != remote.url() || profile.Auth != state.GitAuthToken || !profile.HasCredential {
		t.Errorf("profile = %+v, want the URL, the token and a stored login", profile)
	}
	if profile.ResolvedURL != remote.url() {
		t.Errorf("resolved URL = %q, want the https one as stored", profile.ResolvedURL)
	}

	// No answer of this API carries the login itself.
	for _, rec := range []*httptest.ResponseRecorder{
		gitRequest(t, srv, http.MethodGet, "/v1/projects/"+p.Name+"/git", ""),
	} {
		if strings.Contains(rec.Body.String(), "the-token") {
			t.Errorf("the answer carries the token: %s", rec.Body.String())
		}
	}

	// Public signs in as a stored GitHub account, with no secret in the body.
	body = `{"url":"https://github.com/o/pawly.git","auth":"github-account","credential":"me"}`
	pg = gitResponse(t, gitRequest(t, srv, http.MethodPut, "/v1/projects/"+p.Name+"/git/public", body))
	if len(pg.Profiles) != 2 || pg.Profiles[1].Profile != state.GitProfilePublic || !pg.Profiles[1].HasCredential {
		t.Fatalf("after set public: %+v", pg)
	}

	// The project can move to it, and only then does it stop using private.
	if pg.AgentRemote != state.GitProfilePrivate {
		t.Fatalf("agent remote = %q, want private before use", pg.AgentRemote)
	}
	pg = gitResponse(t, gitRequest(t, srv, http.MethodPost, "/v1/projects/"+p.Name+"/git/use", `{"profile":"public"}`))
	if pg.AgentRemote != state.GitProfilePublic {
		t.Errorf("after use public, agent remote = %q", pg.AgentRemote)
	}
	if row, err := srv.store.Project(ctx, p.Name); err != nil || row.AgentRemote != state.GitProfilePublic {
		t.Errorf("stored agent remote = %q, %v", row.AgentRemote, err)
	}

	// The test asks the remote with the profile's login, and the remote sees
	// exactly what the profile stores.
	rec := gitRequest(t, srv, http.MethodPost, "/v1/projects/"+p.Name+"/git/test", `{"profile":"private"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var result api.ProjectGitTest
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Profile != state.GitProfilePrivate || result.URL != remote.url() {
		t.Errorf("test = %+v, want the private profile's remote accepted", result)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:the-token"))
	if got := remote.login(); got != want {
		t.Errorf("the remote was given %q, want %q", got, want)
	}
	if strings.Contains(rec.Body.String(), "the-token") {
		t.Errorf("the test's answer carries the token: %s", rec.Body.String())
	}

	// Removing the profile removes the login it was the only user of.
	pg = gitResponse(t, gitRequest(t, srv, http.MethodDelete, "/v1/projects/"+p.Name+"/git/private", ""))
	if len(pg.Profiles) != 1 || pg.Profiles[0].Profile != state.GitProfilePublic {
		t.Errorf("after remove: %+v", pg)
	}
	if kind, secret, err := creds.ProjectGitCredential(ctx, p.Name, "private"); err != nil || kind != "" || secret != "" {
		t.Errorf("the removed profile's credential is still stored: %q %q %v", kind, secret, err)
	}
	// The account's token is the account's: removing the profile keeps it.
	if token, err := creds.GitHubToken("me"); err != nil || token != "gh-token" {
		t.Errorf("removing the public profile dropped the GitHub account: %q, %v", token, err)
	}
	if rec := gitRequest(t, srv, http.MethodDelete, "/v1/projects/"+p.Name+"/git/private", ""); rec.Code == http.StatusOK {
		t.Errorf("removing a profile that isn't there answered 200: %s", rec.Body.String())
	}
}

// TestProjectGitTestSaysWhenTheLoginIsMissing checks the refusal a project
// with a profile and no login gets, which is what project add leaves behind,
// and that it names the command that stores one rather than letting a pod fail
// later.
func TestProjectGitTestSaysWhenTheLoginIsMissing(t *testing.T) {
	srv, p := gitTestServer(t)
	body := `{"url":"https://hub.example/pawly.git","auth":"token"}`
	pg := gitResponse(t, gitRequest(t, srv, http.MethodPut, "/v1/projects/"+p.Name+"/git/private", body))
	if pg.Profiles[0].HasCredential || pg.Profiles[0].Credential != state.GitProfilePrivate {
		t.Fatalf("profile = %+v, want the profile's own name and no login", pg.Profiles[0])
	}

	rec := gitRequest(t, srv, http.MethodPost, "/v1/projects/"+p.Name+"/git/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var result api.ProjectGitTest
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Error("test = ok for a profile whose login is not stored")
	}
	for _, want := range []string{"agentbox project git pawly set private", "--credential private --from-file <file>"} {
		if !strings.Contains(result.Detail, want) {
			t.Errorf("detail %q doesn't say %q", result.Detail, want)
		}
	}
}

// TestProjectGitUseRefusesAMissingProfile checks that the one way a project
// can be left unable to clone is refused where it is asked for.
func TestProjectGitUseRefusesAMissingProfile(t *testing.T) {
	srv, p := gitTestServer(t)
	rec := gitRequest(t, srv, http.MethodPost, "/v1/projects/"+p.Name+"/git/use", `{"profile":"public"}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("use public with no public profile answered 200: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "has no public git profile") {
		t.Errorf("the refusal doesn't say which profile is missing: %s", rec.Body.String())
	}
}

// TestAddProjectFillsItsGitProfiles is project add reading the checkout's two
// remotes: hub becomes private, origin becomes public, and a public profile
// without a GitHub account is a token waiting for one.
func TestAddProjectFillsItsGitProfiles(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, "")
	if err := srv.gitCreds().SaveGitHubToken("me", "gh-token"); err != nil {
		t.Fatal(err)
	}
	root := testutil.FixtureRepo(t, "hello-stack")
	testutil.Git(t, root, "remote", "add", "hub", "https://hub.example/h4ad/hello-stack.git")
	testutil.Git(t, root, "remote", "add", "origin", "git@github.com:leciric/hello-stack.git")

	body, err := json.Marshal(api.AddProjectRequest{Path: root, Name: "hello-stack", GitHubAccount: "me"})
	if err != nil {
		t.Fatal(err)
	}
	rec := gitRequest(t, srv, http.MethodPost, "/v1/projects", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	p, err := srv.store.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	remotes, err := srv.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 2 {
		t.Fatalf("profiles = %+v, want private and public", remotes)
	}
	private, public := remotes[0], remotes[1]
	if private.Profile != state.GitProfilePrivate || private.URL != "https://hub.example/h4ad/hello-stack.git" || private.Auth != state.GitAuthToken || private.Credential != "" {
		t.Errorf("private = %+v, want hub as a token profile with no login yet", private)
	}
	if public.Profile != state.GitProfilePublic || public.URL != "git@github.com:leciric/hello-stack.git" ||
		public.Auth != state.GitAuthGitHubAccount || public.Credential != "me" {
		t.Errorf("public = %+v, want origin as the project's GitHub account", public)
	}

	// Adding the project twice is not what fills these: a later prefill, after
	// the remotes moved, leaves what the user has alone.
	testutil.Git(t, root, "remote", "set-url", "hub", "https://hub.example/h4ad/elsewhere.git")
	srv.prefillGitProfiles(ctx, p)
	remotes, err = srv.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if remotes[0].URL != "https://hub.example/h4ad/hello-stack.git" {
		t.Errorf("a second prefill overwrote the profile: %+v", remotes[0])
	}
}

// TestAddProjectWithOriginOffGitHub keeps the project's GitHub account off a
// forge that isn't GitHub: origin there becomes a token profile, whose login
// the user stores for that host, never the account's token sent to it.
func TestAddProjectWithOriginOffGitHub(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, "")
	if err := srv.gitCreds().SaveGitHubToken("me", "gh-token"); err != nil {
		t.Fatal(err)
	}
	root := testutil.FixtureRepo(t, "hello-stack")
	testutil.Git(t, root, "remote", "add", "origin", "https://gitlab.com/leciric/hello-stack.git")

	body, err := json.Marshal(api.AddProjectRequest{Path: root, Name: "hello-stack", GitHubAccount: "me"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := gitRequest(t, srv, http.MethodPost, "/v1/projects", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	remotes, err := srv.store.ProjectGitRemotes(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 || remotes[0].Profile != state.GitProfilePublic || remotes[0].Auth != state.GitAuthToken || remotes[0].Credential != "" {
		t.Errorf("profiles = %+v, want origin as a token profile with no login", remotes)
	}
}

// TestAddProjectWithoutAGitHubAccount checks the public profile a project with
// no GitHub account gets: origin as a token profile, so a token added later is
// all a public agent needs.
func TestAddProjectWithoutAGitHubAccount(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, "")
	root := testutil.FixtureRepo(t, "hello-stack")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/leciric/hello-stack.git")

	body, err := json.Marshal(api.AddProjectRequest{Path: root, Name: "hello-stack"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := gitRequest(t, srv, http.MethodPost, "/v1/projects", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	p, err := srv.store.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	remotes, err := srv.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 || remotes[0].Profile != state.GitProfilePublic || remotes[0].Auth != state.GitAuthToken {
		t.Errorf("profiles = %+v, want origin as a token profile", remotes)
	}
}

// TestProjectGitSetKeepsWhatTheRequestLeavesOut is the URL-only edit: an
// ssh-key profile stays ssh-key, and a shared login stays attached.
func TestProjectGitSetKeepsWhatTheRequestLeavesOut(t *testing.T) {
	srv, p := gitTestServer(t)
	put := func(body string) api.ProjectGitProfile {
		t.Helper()
		pg := gitResponse(t, gitRequest(t, srv, http.MethodPut, "/v1/projects/"+p.Name+"/git/private", body))
		if len(pg.Profiles) != 1 {
			t.Fatalf("profiles = %+v", pg.Profiles)
		}
		return pg.Profiles[0]
	}
	put(`{"url":"git@example.com:o/a.git","auth":"ssh-key","credential":"shared","secret":"-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n"}`)
	got := put(`{"url":"git@example.com:o/b.git"}`)
	if got.URL != "git@example.com:o/b.git" || got.Auth != state.GitAuthSSHKey || got.Credential != "shared" {
		t.Errorf("after a URL-only set: %+v, want ssh-key on the shared login", got)
	}
	// Naming another auth starts from that auth's default credential.
	got = put(`{"url":"https://example.com/o/b.git","auth":"token"}`)
	if got.Auth != state.GitAuthToken || got.Credential != "private" {
		t.Errorf("after --auth token: %+v, want token on the profile's own login", got)
	}
}

// TestGitLsRemoteTakesTheURLAsAURL is the payload a URL starting with a dash
// carried: without "--", git ls-remote read it as --upload-pack=<command> and
// ran that command against the working directory's own origin. state refuses
// such a URL when it is stored; this is the second wall.
func TestGitLsRemoteTakesTheURLAsAURL(t *testing.T) {
	testutil.GitEnv(t)
	t.Setenv("GIT_SSH_COMMAND", "false")
	bare := t.TempDir()
	testutil.Git(t, bare, "init", "--quiet", "--bare")
	work := t.TempDir()
	testutil.Git(t, work, "init", "--quiet")
	testutil.Git(t, work, "remote", "add", "origin", bare)
	t.Chdir(work)

	const payload = "--upload-pack=touch${IFS}pwned;git-upload-pack@h:p"
	_, detail, err := gitLsRemote(context.Background(), payload, state.GitAuthToken, "a-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(work, "pwned")); statErr == nil {
		t.Fatal("git ran the URL's --upload-pack command")
	}
	if detail == "" {
		t.Error("ls-remote of the payload succeeded, want git's refusal")
	}
}

// TestGitLsRemoteSendsTheTokenOnlyToItsHost is git test against a remote that
// redirects to another host: the helper is scoped to the URL's host and checks
// the host git asks about, so the other host never sees the token. The remote
// that asks for a login on its own host gets it, which is what shows the
// helper is there at all.
func TestGitLsRemoteSendsTheTokenOnlyToItsHost(t *testing.T) {
	testutil.GitEnv(t)
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	var mu sync.Mutex
	var seen []string
	asks := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if auth := r.Header.Get("Authorization"); auth != "" {
				mu.Lock()
				seen = append(seen, name+" "+auth)
				mu.Unlock()
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}
	other := httptest.NewTLSServer(asks("other"))
	t.Cleanup(other.Close)
	// The same server under another name is another host to git.
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	redirecting := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherURL+r.URL.RequestURI(), http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	own := httptest.NewTLSServer(asks("own"))
	t.Cleanup(own.Close)

	if _, detail, err := gitLsRemote(context.Background(), redirecting.URL+"/o/r.git", state.GitAuthToken, "a-token"); err != nil || detail == "" {
		t.Fatalf("ls-remote through the redirect = %q, %v, want git's failure", detail, err)
	}
	if _, _, err := gitLsRemote(context.Background(), own.URL+"/o/r.git", state.GitAuthToken, "a-token"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := "own Basic " + base64.StdEncoding.EncodeToString([]byte(gitTestTokenUser+":a-token"))
	for _, s := range seen {
		if !strings.HasPrefix(s, "own ") {
			t.Errorf("another host was sent a login: %s", s)
		}
	}
	if len(seen) == 0 || seen[len(seen)-1] != want {
		t.Errorf("logins seen = %q, want the remote's own host to get %q", seen, want)
	}
}
