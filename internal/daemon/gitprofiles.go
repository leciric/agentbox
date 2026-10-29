package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/gitcred"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// The project git profiles routes: which remote a project's cluster agents
// clone and push with, and the login each one signs in with. Everything here
// follows the one rule secrets already follow: a response carries the name of
// a credential and whether it is stored, never its value.

// gitTestTokenUser is the user a token answers as, which must be the same one
// an agent's own pod uses (agent.gitprofile).
const gitTestTokenUser = "x-access-token"

// projectGit is GET /v1/projects/{project}/git: both profiles and the one the
// project's agents use.
func (s *Server) projectGit(w http.ResponseWriter, r *http.Request) error {
	out, err := s.projectGitInfo(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// projectGitInfo is a project's profiles as the API answers them.
func (s *Server) projectGitInfo(ctx context.Context, project string) (api.ProjectGit, error) {
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return api.ProjectGit{}, err
	}
	remotes, err := s.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		return api.ProjectGit{}, err
	}
	out := api.ProjectGit{Profiles: []api.ProjectGitProfile{}, AgentRemote: agentRemoteOrPrivate(p.AgentRemote)}
	for _, remote := range remotes {
		out.Profiles = append(out.Profiles, s.projectGitProfileInfo(ctx, p, remote))
	}
	return out, nil
}

// agentRemoteOrPrivate is the profile a project's agents use when its row names none:
// the default the command line and the app show.
func agentRemoteOrPrivate(profile string) string {
	if profile == "" {
		return state.GitProfilePrivate
	}
	return profile
}

// projectGitProfileInfo is one profile, with whether its login is stored and
// the URL a pod would be given. Neither carries the credential.
func (s *Server) projectGitProfileInfo(ctx context.Context, p state.Project, r state.GitRemote) api.ProjectGitProfile {
	info := api.ProjectGitProfile{Profile: r.Profile, URL: r.URL, Auth: r.Auth, Credential: r.Credential}
	creds := s.gitCreds()
	if r.Auth == state.GitAuthGitHubAccount {
		token, err := creds.GitHubToken(r.Credential)
		info.HasCredential = err == nil && token != ""
	} else if r.Credential != "" {
		kind, secret, err := creds.ProjectGitCredential(ctx, p.Name, r.Credential)
		info.HasCredential = err == nil && secret != "" && kind == gitCredentialKind(r.Auth)
	}
	if resolved, err := agent.ResolveGitProfileURL(ctx, r); err != nil {
		info.ResolveError = err.Error()
	} else {
		info.ResolvedURL = resolved
	}
	return info
}

// setProjectGit is PUT /v1/projects/{project}/git/{profile}: the profile, and
// the login its secret names when one is given, written by one request so a
// profile never exists without the login it was saved with.
func (s *Server) setProjectGit(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	var req api.SetProjectGitProfileRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	auth := strings.TrimSpace(req.Auth)
	credential := strings.TrimSpace(req.Credential)
	// A profile that exists keeps what the request leaves out, so changing its
	// URL alone doesn't turn an ssh-key profile into a token one or detach a
	// shared login. A new auth with no credential starts from the default name.
	if remotes, err := s.store.ProjectGitRemotes(r.Context(), p.Name); err == nil {
		if old, ok := gitRemoteNamed(remotes, r.PathValue("profile")); ok {
			if auth == "" {
				auth = old.Auth
				if credential == "" {
					credential = old.Credential
				}
			} else if credential == "" && auth == old.Auth {
				credential = old.Credential
			}
		}
	}
	if auth == "" {
		auth = state.GitAuthToken
	}
	if credential == "" && auth != state.GitAuthGitHubAccount {
		// A token or a key is stored under a name of its own, and the
		// profile's name is the one to use when the user didn't pick one. A
		// GitHub account already has a name, and guessing it would hide the
		// mistake: state.GitRemote refuses a github-account profile with none.
		credential = r.PathValue("profile")
	}
	// Refused before anything is stored: a GitHub account's token is the
	// account's, and "set the account's token here" would only hide that.
	if req.Secret != "" && auth == state.GitAuthGitHubAccount {
		return fmt.Errorf("project %q's %s git profile signs in as a GitHub account, whose token is not a git credential: store it with agentbox auth github, or use --auth token", p.Name, r.PathValue("profile"))
	}
	remote := state.GitRemote{
		Project:    p.Name,
		Profile:    r.PathValue("profile"),
		URL:        strings.TrimSpace(req.URL),
		Auth:       auth,
		Credential: credential,
	}
	// The login is written first, so a profile never names one a failed write
	// left unstored. A login whose profile write then fails is invisible and
	// harmless: nothing names it.
	if req.Secret != "" {
		if err := s.gitCreds().SetProjectGitCredential(r.Context(), p.Name, credential, gitCredentialKind(auth), req.Secret); err != nil {
			return err
		}
	}
	if err := s.store.SetProjectGitRemote(r.Context(), remote); err != nil {
		return err
	}
	return s.writeProjectGit(w, r.Context(), p.Name)
}

// removeProjectGit is DELETE /v1/projects/{project}/git/{profile}. The login
// goes with it only when no profile the project keeps still names it.
func (s *Server) removeProjectGit(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	profile := r.PathValue("profile")
	remotes, err := s.store.ProjectGitRemotes(r.Context(), p.Name)
	if err != nil {
		return err
	}
	removed, ok := gitRemoteNamed(remotes, profile)
	if !ok {
		return fmt.Errorf("project %q has no %s git profile: %w", p.Name, profile, state.ErrNotFound)
	}
	if err := s.store.RemoveProjectGitRemote(r.Context(), p.Name, profile); err != nil {
		return err
	}
	if removed.Auth != state.GitAuthGitHubAccount && removed.Credential != "" && !gitCredentialNamed(remotes, profile, removed.Credential) {
		if err := s.gitCreds().RemoveProjectGitCredential(r.Context(), p.Name, removed.Credential); err != nil {
			s.logf("%s: removing git credential %s: %v", p.Name, removed.Credential, err)
		}
	}
	return s.writeProjectGit(w, r.Context(), p.Name)
}

// useProjectGit is POST /v1/projects/{project}/git/use: which profile its
// agents clone with from now on.
func (s *Server) useProjectGit(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	var req api.UseProjectGitRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.store.SetProjectAgentRemote(r.Context(), p.Name, strings.TrimSpace(req.Profile)); err != nil {
		return err
	}
	return s.writeProjectGit(w, r.Context(), p.Name)
}

// testProjectGit is POST /v1/projects/{project}/git/test: the profile's URL
// asked for its refs with the stored login. The login is written to a 0600 file
// in a temporary directory, so it is never an argument, and git's answer is
// redacted before it is returned.
func (s *Server) testProjectGit(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := s.store.Project(ctx, r.PathValue("project"))
	if err != nil {
		return err
	}
	var req api.TestProjectGitRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	remotes, err := s.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		return err
	}
	var remote state.GitRemote
	profile := strings.TrimSpace(req.Profile)
	if profile == "" {
		if remote, err = agent.SelectGitProfile(p, remotes); err != nil {
			return err
		}
	} else {
		named, ok := gitRemoteNamed(remotes, profile)
		if !ok {
			return fmt.Errorf("project %q has no %s git profile: run agentbox project git %s set %s <url> --auth token --credential <name> --from-file <file>", p.Name, profile, p.Name, profile)
		}
		remote = named
	}
	out := api.ProjectGitTest{Profile: remote.Profile, URL: remote.URL}
	resolved, err := agent.ResolveGitProfileURL(ctx, remote)
	if err != nil {
		out.Detail = err.Error()
		return writeJSON(w, http.StatusOK, out)
	}
	out.URL = resolved
	secret, detail, err := s.gitProfileSecret(ctx, p.Name, remote)
	if err != nil {
		return err
	}
	if detail != "" {
		out.Detail = detail
		return writeJSON(w, http.StatusOK, out)
	}
	refs, detail, err := gitLsRemote(ctx, resolved, remote.Auth, secret)
	if err != nil {
		return err
	}
	out.Refs, out.Detail, out.OK = refs, detail, detail == ""
	return writeJSON(w, http.StatusOK, out)
}

// writeProjectGit answers with every profile the project now has.
func (s *Server) writeProjectGit(w http.ResponseWriter, ctx context.Context, project string) error {
	out, err := s.projectGitInfo(ctx, project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// gitCreds is the store the profiles' logins live in.
func (s *Server) gitCreds() credentials.Store {
	return credentials.Store{Dir: s.cfg.Paths.Credentials()}
}

// gitCredentialKind is the kind of git credential an auth kind stores, "" for
// an auth kind that isn't one.
func gitCredentialKind(auth string) string {
	switch auth {
	case state.GitAuthToken:
		return credentials.GitCredentialToken
	case state.GitAuthSSHKey:
		return credentials.GitCredentialSSHKey
	}
	return ""
}

// gitProfileSecret is the login a profile signs in with: the secret itself, or
// a sentence naming what to store when it isn't there. The sentence never needs
// to name the profile: the caller already did.
func (s *Server) gitProfileSecret(ctx context.Context, project string, r state.GitRemote) (secret, detail string, err error) {
	creds := s.gitCreds()
	if r.Auth == state.GitAuthGitHubAccount {
		token, err := creds.GitHubToken(r.Credential)
		if err != nil {
			return "", "", err
		}
		if token == "" {
			return "", fmt.Sprintf("GitHub account %q has no stored token: store it with agentbox auth github", r.Credential), nil
		}
		return token, "", nil
	}
	credential := r.Credential
	if credential == "" {
		// A profile that was never given a login names the profile itself as
		// the name to store one under, which is what the command line does.
		credential = r.Profile
	}
	var kind, stored string
	if r.Credential != "" {
		var err error
		kind, stored, err = creds.ProjectGitCredential(ctx, project, credential)
		if err != nil {
			return "", "", err
		}
	}
	if stored == "" {
		return "", fmt.Sprintf("credential %q is not stored: run agentbox project git %s set %s %s --auth %s --credential %s --from-file <file>", credential, project, r.Profile, r.URL, r.Auth, credential), nil
	}
	if want := gitCredentialKind(r.Auth); kind != want {
		return "", fmt.Sprintf("credential %q holds a %s, and the profile signs in with a %s", credential, kind, r.Auth), nil
	}
	return stored, "", nil
}

// gitLsRemote asks a URL for its refs with the profile's login. The helper is
// a 0600 file in a directory of its own, and ssh gets GIT_SSH_COMMAND: the
// secret is never an argument, so it can't be read out of this machine's
// process list.
func gitLsRemote(ctx context.Context, resolved, auth, secret string) (refs int, detail string, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "agentbox-git-test-")
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	args := []string{}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	switch auth {
	case state.GitAuthSSHKey:
		key := filepath.Join(dir, "key")
		if err := os.WriteFile(key, []byte(strings.TrimSpace(secret)+"\n"), 0o600); err != nil {
			return 0, "", err
		}
		env = append(env, "GIT_SSH_COMMAND="+gitcred.SSHCommand(key))
	default:
		// A token answers an https URL, which is what ResolveGitProfileURL
		// leaves for this auth kind.
		host := ""
		if u, err := neturl.Parse(resolved); err == nil {
			host = u.Host
		}
		helper := filepath.Join(dir, "credential")
		content := gitcred.CredentialHelper(host, gitTestTokenUser, secret)
		if err := os.WriteFile(helper, []byte(content), 0o600); err != nil {
			return 0, "", err
		}
		args = append(args, "-c", "credential.helper=", "-c", "credential.helper=!sh "+helper)
	}
	args = append(args, "ls-remote", resolved)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return 0, gitcred.Redact(detail, secret), nil
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if strings.TrimSpace(line) != "" {
			refs++
		}
	}
	return refs, "", nil
}

// gitRemoteNamed is the profile called profile, if the project has it.
func gitRemoteNamed(remotes []state.GitRemote, profile string) (state.GitRemote, bool) {
	for _, r := range remotes {
		if r.Profile == profile {
			return r, true
		}
	}
	return state.GitRemote{}, false
}

// gitCredentialNamed reports whether a profile other than removed still signs
// in with credential, in which case removing the profile must not remove the
// login another one uses.
func gitCredentialNamed(remotes []state.GitRemote, removed, credential string) bool {
	for _, r := range remotes {
		if r.Profile != removed && r.Credential == credential && r.Auth != state.GitAuthGitHubAccount {
			return true
		}
	}
	return false
}

// prefillGitProfiles fills a new project's two profiles from the remotes its
// checkout has: hub is where work in progress goes, origin is GitHub. A
// missing remote is not a problem, and a profile the project already has is
// never overwritten, so this is safe to run again.
func (s *Server) prefillGitProfiles(ctx context.Context, p state.Project) {
	have, err := s.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		s.logf("%s: reading its git profiles: %v", p.Name, err)
		return
	}
	used := make(map[string]bool, len(have))
	for _, r := range have {
		used[r.Profile] = true
	}
	set := func(profile, remote, auth, credential string) {
		if used[profile] {
			return
		}
		url, err := gitrepo.RemoteURL(p.Root, remote)
		if errors.Is(err, gitrepo.ErrNoRemote) || (err == nil && strings.TrimSpace(url) == "") {
			return
		}
		if err != nil {
			s.logf("%s: reading its %s remote: %v", p.Name, remote, err)
			return
		}
		if err := s.store.SetProjectGitRemote(ctx, state.GitRemote{
			Project: p.Name, Profile: profile, URL: url, Auth: auth, Credential: credential,
		}); err != nil {
			// A remote that can't be a profile, such as a local path, is the
			// user's to fix in "project git set": adding the project worked.
			s.logf("%s: its %s remote is not a git profile: %v", p.Name, remote, err)
		}
	}
	set(state.GitProfilePrivate, "hub", state.GitAuthToken, "")
	publicAuth, publicCredential := state.GitAuthToken, ""
	if p.GitHubAccount != "" {
		publicAuth, publicCredential = state.GitAuthGitHubAccount, p.GitHubAccount
	}
	set(state.GitProfilePublic, "origin", publicAuth, publicCredential)
}
