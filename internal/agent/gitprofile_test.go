package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"agentbox/internal/credentials"
	"agentbox/internal/gitcred"
	mach "agentbox/internal/machine"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// gitRemote is one profile of the test project.
func gitRemote(project, profile, url, auth, credential string) state.GitRemote {
	return state.GitRemote{Project: project, Profile: profile, URL: url, Auth: auth, Credential: credential}
}

// TestSelectGitProfile is which of a project's two profiles its agents clone
// with, and the refusal when it has neither.
func TestSelectGitProfile(t *testing.T) {
	private := gitRemote("p", state.GitProfilePrivate, "https://hub.example/p.git", state.GitAuthToken, "private")
	public := gitRemote("p", state.GitProfilePublic, "https://github.com/o/p.git", state.GitAuthGitHubAccount, "me")

	for _, tc := range []struct {
		name        string
		agentRemote string
		remotes     []state.GitRemote
		want        string
		wantErr     []string
	}{
		{name: "private is the default", remotes: []state.GitRemote{private, public}, want: state.GitProfilePrivate},
		{name: "the project's choice", agentRemote: state.GitProfilePublic, remotes: []state.GitRemote{private, public}, want: state.GitProfilePublic},
		{name: "a removed public falls back to private", agentRemote: state.GitProfilePublic, remotes: []state.GitRemote{private}, want: state.GitProfilePrivate},
		{name: "a project with only public uses it", remotes: []state.GitRemote{public}, want: state.GitProfilePublic},
		{
			name:    "neither profile",
			remotes: nil,
			wantErr: []string{
				`project "p" has no private git profile`,
				"agentbox project git p set private",
			},
		},
		{
			name:        "neither profile, and public is the one chosen",
			agentRemote: state.GitProfilePublic,
			wantErr: []string{
				`project "p" has no public git profile`,
				"agentbox project git p set public",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := state.Project{Name: "p", AgentRemote: tc.agentRemote}
			got, err := SelectGitProfile(p, tc.remotes)
			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("SelectGitProfile() = %+v, want an error", got)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q doesn't say %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Profile != tc.want {
				t.Errorf("profile = %q, want %q", got.Profile, tc.want)
			}
		})
	}
}

// TestResolveGitProfileReadsTheLogin is the profile a pod is given: the URL
// alias-free, the host its credential answers for, and the secret itself, or
// the refusal that names the command which stores one.
func TestResolveGitProfileReadsTheLogin(t *testing.T) {
	testutil.GitEnv(t)
	ctx := context.Background()
	st := openInternalStore(t)
	if err := st.AddProject(ctx, state.Project{Name: "proj", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: t.TempDir()}
	m := &Manager{Store: st, Creds: creds}
	p, err := st.Project(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}

	// A token profile whose credential nobody stored is refused by name, and a
	// profile that was never given one names the profile as the name to store.
	if err := st.SetProjectGitRemote(ctx, gitRemote("proj", state.GitProfilePrivate, "https://hub.example/p.git", state.GitAuthToken, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.resolveGitProfile(ctx, p); err == nil {
		t.Fatal("resolveGitProfile() = nil, want the missing credential refused")
	} else if !strings.Contains(err.Error(), "--credential private --from-file <file>") {
		t.Errorf("error %q doesn't say how to store a login for the profile", err)
	}
	if err := st.SetProjectGitRemote(ctx, gitRemote("proj", state.GitProfilePrivate, "https://hub.example/p.git", state.GitAuthToken, "private")); err != nil {
		t.Fatal(err)
	}
	_, err = m.resolveGitProfile(ctx, p)
	if err == nil {
		t.Fatal("resolveGitProfile() = nil, want the missing credential refused")
	}
	for _, want := range []string{`project "proj"'s private git profile`, `credential "private"`, "--from-file <file>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't say %q", err, want)
		}
	}

	// Stored, it is the profile the pod gets, with the host its helper answers
	// for and the secret in hand.
	if err := creds.SetProjectGitCredential(ctx, "proj", "private", credentials.GitCredentialToken, "the-token"); err != nil {
		t.Fatal(err)
	}
	got, err := m.resolveGitProfile(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://hub.example/p.git" || got.Host != "hub.example" {
		t.Errorf("resolved profile = URL %q, host %q", got.URL, got.Host)
	}
	if got.Token != "the-token" {
		t.Errorf("token = %q, want the stored one", got.Token)
	}

	// The wrong kind of credential is refused where the user can see it.
	if err := creds.SetProjectGitCredential(ctx, "proj", "private", credentials.GitCredentialSSHKey, "-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----"); err != nil {
		t.Fatal(err)
	}
	_, err = m.resolveGitProfile(ctx, p)
	if err == nil || !strings.Contains(err.Error(), "holds a ssh-key") {
		t.Errorf("resolveGitProfile() with the wrong credential kind = %v, want it refused", err)
	}

	// A GitHub account with no token names the login, and the token profile
	// that avoids needing one.
	if err := st.SetProjectGitRemote(ctx, gitRemote("proj", state.GitProfilePrivate, "https://github.com/o/p.git", state.GitAuthGitHubAccount, "me")); err != nil {
		t.Fatal(err)
	}
	_, err = m.resolveGitProfile(ctx, p)
	if err == nil {
		t.Fatal("resolveGitProfile() = nil, want the missing GitHub token refused")
	}
	for _, want := range []string{`GitHub account "me"`, "agentbox auth github", "--from-file <file>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't say %q", err, want)
		}
	}
}

// TestResolveGitProfileURLTurnsSSHIntoHTTPSForAToken is the private profile's
// usual shape: the user's own remote is ssh, and the token the profile signs in
// with is answered over https on the same host.
func TestResolveGitProfileURLTurnsSSHIntoHTTPSForAToken(t *testing.T) {
	testutil.GitEnv(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, auth, raw, want string
	}{
		{name: "ssh url", auth: state.GitAuthToken, raw: "ssh://git@hub.example:2222/o/p.git", want: "https://hub.example/o/p.git"},
		{name: "ssh shorthand", auth: state.GitAuthGitHubAccount, raw: "git@github.com:o/p.git", want: "https://github.com/o/p.git"},
		{name: "https is left alone", auth: state.GitAuthToken, raw: "https://hub.example/o/p.git", want: "https://hub.example/o/p.git"},
		{name: "an ssh key keeps ssh", auth: state.GitAuthSSHKey, raw: "ssh://git@hub.example/o/p.git", want: "ssh://git@hub.example/o/p.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveGitProfileURL(ctx, gitRemote("p", state.GitProfilePrivate, tc.raw, tc.auth, "login"))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("ResolveGitProfileURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestResolveGitProfileURLRefusesATokenOverHTTP: a token is sent only over
// https, whatever the stored URL was, and an ssh key may still use any URL.
func TestResolveGitProfileURLRefusesATokenOverHTTP(t *testing.T) {
	testutil.GitEnv(t)
	ctx := context.Background()
	for _, auth := range []string{state.GitAuthToken, state.GitAuthGitHubAccount} {
		for _, raw := range []string{"http://host.k3d.internal:3000/o/p.git", "http://github.com/o/p.git", "git://hub.example/o/p.git"} {
			_, err := ResolveGitProfileURL(ctx, gitRemote("p", state.GitProfilePrivate, raw, auth, "login"))
			if err == nil || !strings.Contains(err.Error(), "only over https") {
				t.Errorf("ResolveGitProfileURL(%s, %q) = %v, want it refused", auth, raw, err)
			}
		}
	}
	if _, err := ResolveGitProfileURL(ctx, gitRemote("p", state.GitProfilePrivate, "ssh://git@hub.example/o/p.git", state.GitAuthSSHKey, "deploy")); err != nil {
		t.Errorf("an ssh key was refused: %v", err)
	}
}

// TestResolveGitProfileURLSendsAGitHubAccountOnlyToGitHub: a GitHub account's
// token never answers another host, however the URL names it, so neither the
// pod's helper nor `git test` ever holds it for that host.
func TestResolveGitProfileURLSendsAGitHubAccountOnlyToGitHub(t *testing.T) {
	testutil.GitEnv(t)
	ctx := context.Background()
	for _, raw := range []string{
		"https://gitlab.com/o/p.git",
		"git@gitlab.com:o/p.git",
		"https://github.com.evil.example/o/p.git",
		"https://hub.example/o/p.git",
	} {
		_, err := ResolveGitProfileURL(ctx, gitRemote("p", state.GitProfilePublic, raw, state.GitAuthGitHubAccount, "me"))
		if err == nil || !strings.Contains(err.Error(), "sent only to GitHub") {
			t.Errorf("ResolveGitProfileURL(%q) = %v, want a refusal", raw, err)
		}
	}
	if _, err := ResolveGitProfileURL(ctx, gitRemote("p", state.GitProfilePublic, "https://GitHub.com/o/p.git", state.GitAuthGitHubAccount, "me")); err != nil {
		t.Errorf("a github.com URL was refused: %v", err)
	}
}

// TestGitHostAliasIsWhereAPodsRemoteAnswers: a name is resolved here and handed
// to the pod, an address needs nothing, and a name this machine can't resolve
// either leaves the pod as it was.
func TestGitHostAliasIsWhereAPodsRemoteAnswers(t *testing.T) {
	ctx := context.Background()
	defer func(previous func(context.Context, string) ([]string, error)) { lookupHost = previous }(lookupHost)
	var asked string
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		asked = host
		switch host {
		case "hub.example.ts.net":
			return []string{"fd7a::1", "203.0.113.7"}, nil
		}
		return nil, errors.New("no such host")
	}
	for _, tc := range []struct {
		name, url string
		want      map[string]string
		lookups   string
	}{
		{
			name:    "a name becomes an address",
			url:     "https://hub.example.ts.net/h4ad/agentbox.git",
			want:    map[string]string{"hub.example.ts.net": "203.0.113.7"},
			lookups: "hub.example.ts.net",
		},
		{
			name: "an address needs nothing, and isn't looked up",
			url:  "ssh://git@192.0.2.10:30143/h4ad/agentbox.git",
		},
		{
			name:    "a name that doesn't resolve leaves the pod alone",
			url:     "https://nowhere.example/o/p.git",
			lookups: "nowhere.example",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked = ""
			got := GitHostAlias(ctx, tc.url)
			if tc.lookups != asked {
				t.Errorf("looked up %q, want %q", asked, tc.lookups)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("GitHostAlias(%q) = %v, want %v", tc.url, got, tc.want)
			}
			for host, addr := range tc.want {
				if got[host] != addr {
					t.Errorf("GitHostAlias(%q)[%q] = %q, want %q", tc.url, host, got[host], addr)
				}
			}
		})
	}
}

// TestPodGitConfig is the exact ~/.gitconfig a pod gets: the project profile's
// login for its host, and none of the ssh-to-https rewriting an ssh profile
// must not have.
func TestPodGitConfig(t *testing.T) {
	testutil.GitEnv(t)
	const hooks = "/home/agent/.config/agentbox/git-hooks"

	// An Incus agent's config is what it has always been: no section for a
	// profile, and its github.com rewrite.
	incus := gitConfig(true, hooks, nil)
	wantIncus := `[core]
	hooksPath = "/home/agent/.config/agentbox/git-hooks"
[credential "https://github.com"]
	helper =
	helper = !gh auth git-credential
[url "https://github.com/"]
	insteadOf = git@github.com:
	insteadOf = ssh://git@github.com/
`
	if incus != wantIncus {
		t.Errorf("Incus .gitconfig:\n%s\nwant:\n%s", incus, wantIncus)
	}

	// A pod with a token profile answers that host with the helper file the
	// master delivered, after the github.com helper, which is reset first.
	token := gitConfig(true, hooks, &gitConfigSection{Scheme: "https", Host: "hub.example", Helper: "!sh " + podGitCredential})
	wantToken := wantIncus + `[credential "https://hub.example"]
	helper =
	helper = "!sh /run/agentbox/git-credential"
`
	if token != wantToken {
		t.Errorf("pod's .gitconfig:\n%s\nwant:\n%s", token, wantToken)
	}
	// The section is keyed on the scheme the clone used, so a later push
	// over the same URL finds its login: a host on a port of its own included.
	port := gitConfig(true, hooks, &gitConfigSection{Scheme: "https", Host: "hub.example:8443", Helper: "!sh " + podGitCredential})
	if want := `[credential "https://hub.example:8443"]`; !strings.Contains(port, want) {
		t.Errorf("pod's .gitconfig:\n%s\nwant the section %s", port, want)
	}
	other := gitConfig(true, hooks, &gitConfigSection{Scheme: "http", Host: "hub.example", Helper: "!sh " + podGitCredential})
	if want := `[credential "http://hub.example"]`; !strings.Contains(other, want) {
		t.Errorf("pod's .gitconfig:\n%s\nwant the section keyed on its own scheme, %s", other, want)
	}

	// An ssh profile names the key instead, and keeps github.com on ssh: the
	// rewrite would send what the key answers to a password prompt.
	ssh := gitConfig(true, hooks, &gitConfigSection{SSHCommand: "ssh -i '/run/agentbox/git-key' -o IdentitiesOnly=yes"})
	wantSSH := `[core]
	hooksPath = "/home/agent/.config/agentbox/git-hooks"
[credential "https://github.com"]
	helper =
	helper = !gh auth git-credential
[core]
	sshCommand = ssh -i '/run/agentbox/git-key' -o IdentitiesOnly=yes
`
	if ssh != wantSSH {
		t.Errorf("pod's .gitconfig for an ssh profile:\n%s\nwant:\n%s", ssh, wantSSH)
	}
	if strings.Contains(ssh, "insteadOf") {
		t.Errorf("an ssh profile's config still rewrites github.com to https:\n%s", ssh)
	}
}

// gitPod is a pod's machine for the git-profile tests: it records the files it
// is given and answers the three git commands the first-start clone runs.
type gitPod struct {
	*podMachineStub
	files  map[string]string
	modes  map[string]os.FileMode
	owners map[string][2]int

	// head says the volume already holds a repository; baseFound says the
	// clone has the base commit.
	head, baseFound bool
	// cloneSays is what git prints on stderr instead of cloning.
	cloneSays string
	commands  []string
}

func newGitPod() *gitPod {
	return &gitPod{
		podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"},
		files:          map[string]string{},
		modes:          map[string]os.FileMode{},
		owners:         map[string][2]int{},
		baseFound:      true,
	}
}

func (m *gitPod) WriteFile(ctx context.Context, path string, content []byte, uid, gid int, mode os.FileMode) error {
	m.files[path], m.modes[path], m.owners[path] = string(content), mode, [2]int{uid, gid}
	return m.podMachineStub.WriteFile(ctx, path, content, uid, gid, mode)
}

func (m *gitPod) UserExec(_ context.Context, _, command string, _ io.Reader, _, stderr io.Writer) error {
	m.commands = append(m.commands, command)
	switch {
	case strings.Contains(command, "rev-parse --verify HEAD"):
		if m.head {
			return nil
		}
		return errors.New("exit status 128")
	case strings.Contains(command, " clone "):
		if m.cloneSays != "" {
			_, _ = io.WriteString(stderr, m.cloneSays)
			return errors.New("exit status 128")
		}
		return nil
	case strings.Contains(command, " cat-file "):
		if m.baseFound {
			return nil
		}
		_, _ = io.WriteString(stderr, "fatal: Not a valid object name")
		return errors.New("exit status 128")
	}
	return nil
}

func (m *gitPod) commandWith(part string) string {
	for _, command := range m.commands {
		if strings.Contains(command, part) {
			return command
		}
	}
	return ""
}

// gitPodRuntime is one cluster whose pods are gitPod.
type gitPodRuntime struct{ m *gitPod }

func (r gitPodRuntime) Kind() mach.Kind             { return mach.KindKubernetes }
func (r gitPodRuntime) Machine(string) mach.Machine { return r.m }

// podAgent is the row a cluster fixture's first agent is configured with.
func (f clusterFixture) podAgent(t *testing.T) state.Agent {
	t.Helper()
	commit, err := f.repo.ResolveCommit("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01",
		Cluster: "k3d", Workspace: "/workspace", Branch: "agentbox/agent-01", BaseCommit: commit,
	}
}

// TestPreparePodGitDeliversTheCredentialAndClones is the first start: the
// token goes into the pod's tmpfs, and the clone is the profile's URL with the
// helper on the command line and the master's base commit checked out.
func TestPreparePodGitDeliversTheCredentialAndClones(t *testing.T) {
	f := newClusterFixture(t, nil)
	pod := newGitPod()
	f.m.Kubernetes = gitPodRuntime{m: pod}
	a := f.podAgent(t)

	section, err := f.m.preparePodGit(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}

	if section == nil || section.Helper != "!sh "+podGitCredential || section.Scheme != "https" || section.Host != "hub.example" {
		t.Fatalf("section = %+v, want the credential helper for hub.example", section)
	}
	// The credential file is the helper script, owned by the pod's agent, and
	// nowhere near the volume that keeps the tree.
	if got := pod.files[podGitCredential]; !strings.Contains(got, "password='pod-token'") || !strings.Contains(got, "username='x-access-token'") {
		t.Errorf("credential file =\n%s\nwant the profile's token for x-access-token", got)
	}
	if pod.modes[podGitCredential] != 0o600 {
		t.Errorf("credential file mode = %o, want 600", pod.modes[podGitCredential])
	}
	if got := pod.owners[podGitCredential]; got != [2]int{1000, 1000} {
		t.Errorf("credential file owner = %v, want the pod's agent 1000:1000", got)
	}
	if _, ok := pod.files["/home/agent/.gitconfig"]; ok {
		t.Error("preparePodGit wrote .gitconfig; configure does that, with the section it is given")
	}

	clone := pod.commandWith("clone --quiet")
	for _, want := range []string{
		"cd '/workspace' && GIT_TERMINAL_PROMPT=0 git",
		"-c credential.helper= -c 'credential.https://hub.example.helper=!sh /run/agentbox/git-credential'",
		"clone --quiet -- 'https://hub.example/hello-stack.git' .",
	} {
		if !strings.Contains(clone, want) {
			t.Errorf("the clone command doesn't have %q:\n%s", want, clone)
		}
	}
	checkout := pod.commandWith("checkout -B")
	for _, want := range []string{"checkout -B 'agentbox/agent-01' '" + a.BaseCommit + "'"} {
		if !strings.Contains(checkout, want) {
			t.Errorf("the checkout command doesn't have %q:\n%s", want, checkout)
		}
	}
}

// TestPreparePodGitDeliversAnSSHKey is the ssh profile's half: the key itself
// goes into the pod's tmpfs, and the clone is told to use it and no other.
func TestPreparePodGitDeliversAnSSHKey(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if err := f.st.SetProjectGitRemote(ctx, state.GitRemote{
		Project: "hello-stack", Profile: state.GitProfilePrivate,
		URL: "ssh://git@hub.example/hello-stack.git", Auth: state.GitAuthSSHKey, Credential: "deploy",
	}); err != nil {
		t.Fatal(err)
	}
	const key = "-----BEGIN OPENSSH PRIVATE KEY-----\nkey-body\n-----END OPENSSH PRIVATE KEY-----"
	if err := f.m.Creds.SetProjectGitCredential(ctx, "hello-stack", "deploy", credentials.GitCredentialSSHKey, key); err != nil {
		t.Fatal(err)
	}
	pod := newGitPod()
	f.m.Kubernetes = gitPodRuntime{m: pod}

	section, err := f.m.preparePodGit(ctx, f.podAgent(t))
	if err != nil {
		t.Fatal(err)
	}
	if section.SSHCommand != gitcred.SSHCommand(podGitKey) {
		t.Errorf("sshCommand = %q, want the key at %s", section.SSHCommand, podGitKey)
	}
	if got := pod.files[podGitKey]; !strings.Contains(got, "key-body") {
		t.Errorf("key file =\n%s\nwant the stored key", got)
	}
	if pod.modes[podGitKey] != 0o600 || pod.owners[podGitKey] != [2]int{1000, 1000} {
		t.Errorf("key file is %o owned by %v, want 600 owned by the pod's agent", pod.modes[podGitKey], pod.owners[podGitKey])
	}
	if _, ok := pod.files[podGitCredential]; ok {
		t.Error("an ssh profile also wrote a token helper")
	}
	clone := pod.commandWith("clone --quiet")
	for _, want := range []string{"core.sshCommand=ssh -i", "clone --quiet -- 'ssh://git@hub.example/hello-stack.git' ."} {
		if !strings.Contains(clone, want) {
			t.Errorf("the clone command doesn't have %q:\n%s", want, clone)
		}
	}
}

// TestPreparePodGitLeavesARepositoryAlone checks the other half of the rule:
// a pod whose volume already holds the tree is never cloned over, because the
// volume is the truth for its uncommitted work.
func TestPreparePodGitLeavesARepositoryAlone(t *testing.T) {
	f := newClusterFixture(t, nil)
	pod := newGitPod()
	pod.head = true
	f.m.Kubernetes = gitPodRuntime{m: pod}

	if _, err := f.m.preparePodGit(context.Background(), f.podAgent(t)); err != nil {
		t.Fatal(err)
	}
	if got := pod.commandWith("clone --quiet"); got != "" {
		t.Errorf("a pod with a repository was cloned over:\n%s", got)
	}
	// Its login is still delivered: a restarted pod has an empty tmpfs.
	if pod.files[podGitCredential] == "" {
		t.Error("a pod with a repository got no credential again")
	}
}

// TestPreparePodGitSaysWhenTheBaseIsNotOnTheProfile checks the error a base
// commit outside the clone produces, and that the token in git's own words is
// redacted out of it.
func TestPreparePodGitSaysWhenTheBaseIsNotOnTheProfile(t *testing.T) {
	f := newClusterFixture(t, nil)
	pod := newGitPod()
	pod.baseFound = false
	f.m.Kubernetes = gitPodRuntime{m: pod}

	_, err := f.m.preparePodGit(context.Background(), f.podAgent(t))
	if err == nil {
		t.Fatal("preparePodGit() = nil, want the missing base commit refused")
	}
	for _, want := range []string{"base commit", "must exist on the remote profile", "hub.example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't say %q", err, want)
		}
	}
}

// TestPodGitErrorsNeverCarryTheSecret checks every failure the profile can
// produce on its way into a pod: git's stderr names the token, and the error
// that reaches the daemon's log and the app must not.
func TestPodGitErrorsNeverCarryTheSecret(t *testing.T) {
	f := newClusterFixture(t, nil)
	pod := newGitPod()
	pod.cloneSays = "fatal: authentication failed for token pod-token"
	f.m.Kubernetes = gitPodRuntime{m: pod}

	_, err := f.m.preparePodGit(context.Background(), f.podAgent(t))
	if err == nil {
		t.Fatal("preparePodGit() = nil, want the clone's failure")
	}
	if strings.Contains(err.Error(), "pod-token") {
		t.Errorf("the error quotes the token: %v", err)
	}
	if !strings.Contains(err.Error(), "authentication failed") || !strings.Contains(err.Error(), "***") {
		t.Errorf("the error lost what git said, or kept the token: %v", err)
	}
}

// TestBuildRefusesAProjectWithNoGitProfile is the refusal before anything is
// made: no row, no pod, and the command that fixes it.
func TestBuildRefusesAProjectWithNoGitProfile(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if err := f.st.RemoveProjectGitRemote(ctx, "hello-stack", state.GitProfilePrivate); err != nil {
		t.Fatal(err)
	}

	_, err := f.m.build(ctx, f.plan(t))
	if err == nil {
		t.Fatal("build() = nil, want the project refused")
	}
	for _, want := range []string{`project "hello-stack" has no private git profile`, "agentbox project git hello-stack set private"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't say %q", err, want)
		}
	}
	var create *CreateError
	if errors.As(err, &create) {
		t.Errorf("the refusal is a create error for %s, so a row was already made: %v", create.Ref, err)
	}
	if _, err := f.st.Agent(ctx, "hello-stack", "agent-01"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the agent's row exists (%v), want the refusal to leave nothing behind", err)
	}
	if len(f.pod.writes) != 0 || f.pod.waits != 0 {
		t.Errorf("the pod was touched (%d writes, %d waits) for a project that can't clone", len(f.pod.writes), f.pod.waits)
	}
}
