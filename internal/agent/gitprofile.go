package agent

// A cluster agent's tree is a clone of one of its project's two git profiles:
// private, where its work-in-progress branches stay, or public, where a finished
// change is published. The pod has no host home and no host identity, so the
// master hands it a resolved URL and the login that URL answers with, one file
// in the pod's tmpfs (k8s.RunDir), and nothing of the user's own.

import (
	"context"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
	"strings"
	"time"

	"agentbox/internal/credentials"
	"agentbox/internal/gitcred"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// Where a pod's git login lives. Both are in agentd's tmpfs, which goes away
// with the pod and is never on the volume that holds the work.
const (
	podGitCredential = k8s.RunDir + "/git-credential"
	podGitKey        = k8s.RunDir + "/git-key"
	// podGitTokenUser is the user a token answers as. GitHub's convention, and
	// Forgejo resolves the token itself whatever user it is given.
	podGitTokenUser = "x-access-token"
)

// gitConfigSection is the pod-only part of ~/.gitconfig: the login that answers
// for the profile's host, either a credential helper or an ssh command.
type gitConfigSection struct {
	// Host is the host the credential answers for, as git matches it:
	// hub.example.com, or host:8443 when the URL names a port.
	Host string
	// Helper is the credential.helper value, "" for an ssh profile.
	Helper string
	// SSHCommand is core.sshCommand, "" for an https profile.
	SSHCommand string
}

// gitProfile is one of a project's profiles resolved for a pod: the URL it
// clones, the host its login answers for, and the secret itself. The secret
// leaves this process only into the pod's tmpfs.
type gitProfile struct {
	state.GitRemote
	// URL is the profile's remote after gitcred.Resolve, with the ssh form
	// turned into https for a token login (see httpsURL).
	URL string
	// Host is the host the login answers for.
	Host string
	// Token is the login for a token and a GitHub-account profile.
	Token string
	// Key is the private key for an ssh-key profile.
	Key string
}

// secret is what must never reach a log, an error or a brief.
func (p *gitProfile) secret() string {
	if p.Token != "" {
		return p.Token
	}
	return p.Key
}

// SelectGitProfile picks the profile a project's agents clone with: the one the
// project names, private when it names none, and the one that is left when the
// named profile has been removed. A project with neither can't have a pod, and
// the error names the command that gives it one, so the refusal is answered by
// the user rather than by a pod that can't clone.
func SelectGitProfile(p state.Project, remotes []state.GitRemote) (state.GitRemote, error) {
	want := p.AgentRemote
	if want == "" {
		want = state.GitProfilePrivate
	}
	if r, ok := gitProfileNamed(remotes, want); ok {
		return r, nil
	}
	// RemoveProjectGitRemote normally moves the project off a profile it is
	// removing, so this is the state an older row or a failed write leaves:
	// any profile is better than refusing to start when one can still clone.
	for _, fallback := range []string{state.GitProfilePrivate, state.GitProfilePublic} {
		if r, ok := gitProfileNamed(remotes, fallback); ok {
			return r, nil
		}
	}
	return state.GitRemote{}, fmt.Errorf("project %q has no %s git profile: run agentbox project git %s set %s <url> --auth token --credential <name> --from-file <file>",
		p.Name, want, p.Name, want)
}

// gitProfileNamed is the profile called profile, if the project has it.
func gitProfileNamed(remotes []state.GitRemote, profile string) (state.GitRemote, bool) {
	for _, r := range remotes {
		if r.Profile == profile {
			return r, true
		}
	}
	return state.GitRemote{}, false
}

// ResolveGitProfileURL is the URL a pod clones for one profile: the stored one
// after this machine's aliases, ssh names and url.<base>.insteadOf rules are
// applied (gitcred.Resolve), so the pod never needs the alias. A token login
// answers an https URL, so an ssh form becomes https for the same host: the
// token is not something ssh can answer with, and the private profile's URL is
// often the ssh remote the user's own checkout uses.
func ResolveGitProfileURL(ctx context.Context, r state.GitRemote) (string, error) {
	resolved, err := gitcred.Resolve(ctx, r.URL, gitcred.Config{})
	if err != nil {
		return "", fmt.Errorf("project %q's %s git profile: %w", r.Project, r.Profile, err)
	}
	if r.Auth == state.GitAuthToken || r.Auth == state.GitAuthGitHubAccount {
		https, changed, err := httpsURL(resolved)
		if err != nil {
			return "", fmt.Errorf("project %q's %s git profile: %w", r.Project, r.Profile, err)
		}
		if changed {
			resolved = https
		}
	}
	// A GitHub account's token is sent only to GitHub: an alias, or a
	// public profile prefilled from an origin on another forge, must not
	// hand it to whichever host the URL names.
	if r.Auth == state.GitAuthGitHubAccount {
		if host := state.GitHostname(resolved); host != state.GitHubHost {
			return "", fmt.Errorf("project %q's %s git profile signs in as GitHub account %q, and %s is on %s, not %s: the account's token is sent only to GitHub, so set the profile with --auth token and a login for that host",
				r.Project, r.Profile, r.Credential, resolved, host, state.GitHubHost)
		}
	}
	return resolved, nil
}

// GitHostAlias is where the host of a git URL answers, for a pod whose own DNS
// has no answer for it: a cluster's pods resolve what its nodes resolve, and a
// private remote is often a name only the daemon knows. Empty when the host is
// already an address, when it doesn't resolve here either, or when it resolves
// to no IPv4.
func GitHostAlias(ctx context.Context, rawURL string) map[string]string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := lookupHost(ctx, host)
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		// The pod's /etc/hosts takes one address, and a host that answers with
		// both families is reachable over IPv4 here: an entry for an IPv6
		// address would only be used if the cluster is.
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			return map[string]string{host: ip.To4().String()}
		}
	}
	return nil
}

// lookupHost is the resolver GitHostAlias uses, a variable so a test can answer
// for a name that isn't in its DNS.
var lookupHost = net.DefaultResolver.LookupHost

// httpsURL turns an ssh URL, or git's ssh shorthand, into the https URL of the same host. A token is what a profile signs in with, and a token is answered
// over https, not ssh: the host's ssh port and user are dropped, because the
// https port is the host's own (443 unless the URL says otherwise, which an ssh
// form can't).
func httpsURL(raw string) (url string, changed bool, err error) {
	if strings.Contains(raw, "://") {
		u, err := neturl.Parse(raw)
		if err != nil {
			return "", false, fmt.Errorf("%q: %w", raw, err)
		}
		switch u.Scheme {
		case "ssh", "git+ssh":
		default:
			return raw, false, nil
		}
		host := u.Hostname()
		if host == "" {
			return "", false, fmt.Errorf("%q has no host", raw)
		}
		u.Scheme, u.Host, u.User = "https", host, nil
		return u.String(), true, nil
	}
	// git's shorthand: [user@]host:path.
	host, repo, ok := strings.Cut(raw, ":")
	if !ok || host == "" || repo == "" || strings.ContainsAny(host, "/ \\") {
		return raw, false, nil
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if host == "" {
		return raw, false, nil
	}
	return "https://" + host + "/" + strings.TrimPrefix(repo, "/"), true, nil
}

// gitHost is the host a URL signs in to, as git matches a credential section:
// the host and the port, never the user.
func gitHost(raw string) string {
	if strings.Contains(raw, "://") {
		u, err := neturl.Parse(raw)
		if err != nil {
			return ""
		}
		return u.Host
	}
	host, _, ok := strings.Cut(raw, ":")
	if !ok {
		return ""
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return host
}

// resolveGitProfile is the profile a project's agents use, with its URL
// resolved and its login read: everything a pod needs, and nothing a pod may
// see of anyone else's.
func (m *Manager) resolveGitProfile(ctx context.Context, p state.Project) (*gitProfile, error) {
	remotes, err := m.Store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	r, err := SelectGitProfile(p, remotes)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveGitProfileURL(ctx, r)
	if err != nil {
		return nil, err
	}
	host := gitHost(resolved)
	if host == "" {
		return nil, fmt.Errorf("project %q's %s git profile: %s names no host to sign in to", p.Name, r.Profile, resolved)
	}
	profile := &gitProfile{GitRemote: r, URL: resolved, Host: host}
	switch r.Auth {
	case state.GitAuthToken, state.GitAuthSSHKey:
		// An empty name is a profile that was never given a login: the create
		// path refuses it with the command that stores one, rather than asking
		// the credential store to look up a name that can't be one.
		var kind, secret string
		if r.Credential != "" {
			kind, secret, err = m.Creds.ProjectGitCredential(ctx, p.Name, r.Credential)
			if err != nil {
				return nil, err
			}
		}
		want := credentials.GitCredentialToken
		if r.Auth == state.GitAuthSSHKey {
			want = credentials.GitCredentialSSHKey
		}
		name := r.Credential
		if name == "" {
			name = r.Profile
		}
		if secret == "" {
			return nil, fmt.Errorf("project %q's %s git profile signs in with credential %q, which is not stored: run agentbox project git %s set %s %s --auth %s --credential %s --from-file <file>",
				p.Name, r.Profile, name, p.Name, r.Profile, r.URL, r.Auth, name)
		}
		if kind != want {
			return nil, fmt.Errorf("project %q's %s git profile is an %s login, and credential %q holds a %s: run agentbox project git %s set %s %s --auth %s --credential %s --from-file <file> again",
				p.Name, r.Profile, r.Auth, name, kind, p.Name, r.Profile, r.URL, r.Auth, name)
		}
		if r.Auth == state.GitAuthToken {
			profile.Token = secret
		} else {
			profile.Key = secret
		}
	case state.GitAuthGitHubAccount:
		token, err := m.Creds.GitHubToken(r.Credential)
		if err != nil {
			return nil, err
		}
		if token == "" {
			return nil, fmt.Errorf("project %q's %s git profile signs in as GitHub account %q, which has no stored token: store it with agentbox auth github, or set a token profile with agentbox project git %s set %s %s --auth token --credential <name> --from-file <file>",
				p.Name, r.Profile, r.Credential, p.Name, r.Profile, r.URL)
		}
		profile.Token = token
	default:
		return nil, fmt.Errorf("project %q's %s git profile: auth %q: use %s, %s or %s",
			p.Name, r.Profile, r.Auth, state.GitAuthToken, state.GitAuthSSHKey, state.GitAuthGitHubAccount)
	}
	return profile, nil
}

// projectGitProfile is resolveGitProfile for a project named by its slug.
func (m *Manager) projectGitProfile(ctx context.Context, project string) (*gitProfile, error) {
	p, err := m.Store.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	return m.resolveGitProfile(ctx, p)
}

// ensurePodGit resolves a's project profile and delivers its login into the
// pod's tmpfs, answering what .gitconfig needs for it. An agent on Incus has
// none of this: its tree is a worktree of this host's repository, and its
// remote is that repository's own.
func (m *Manager) ensurePodGit(ctx context.Context, a state.Agent) (*gitConfigSection, error) {
	if !a.OnCluster() {
		return nil, nil
	}
	p, err := m.projectGitProfile(ctx, a.Project)
	if err != nil {
		return nil, err
	}
	return m.deliverGitProfile(ctx, a, p)
}

// deliverGitProfile writes the profile's login where the pod reads it and
// answers the .gitconfig section that points git at it. The file is mode 0600,
// the pod's agent user's, and in agentd's tmpfs: a stopped pod has neither the
// token nor the key, and the next start writes them again.
func (m *Manager) deliverGitProfile(ctx context.Context, a state.Agent, p *gitProfile) (*gitConfigSection, error) {
	user := m.machineUser(a)
	if p.Key != "" {
		key := p.Key
		if !strings.HasSuffix(key, "\n") {
			key += "\n"
		}
		if err := m.machine(a).WriteFile(ctx, podGitKey, []byte(key), user.UID, user.GID, 0o600); err != nil {
			return nil, podGitError(p, fmt.Errorf("%s's pod: writing the ssh key of its %s git profile: %w", a.Ref(), p.Profile, err))
		}
		return &gitConfigSection{SSHCommand: gitcred.SSHCommand(podGitKey)}, nil
	}
	helper := "!sh " + podGitCredential
	content := gitcred.CredentialHelper(p.Host, podGitTokenUser, p.Token)
	if err := m.machine(a).WriteFile(ctx, podGitCredential, []byte(content), user.UID, user.GID, 0o600); err != nil {
		return nil, podGitError(p, fmt.Errorf("%s's pod: writing the credential of its %s git profile: %w", a.Ref(), p.Profile, err))
	}
	return &gitConfigSection{Host: p.Host, Helper: helper}, nil
}

// preparePodGit is ensurePodGit and the first start's clone: a pod whose volume
// has no repository yet gets one from the profile, on the branch the master
// named, based at the commit the master resolved. Called from configure, which
// is the one point every new pod goes through.
func (m *Manager) preparePodGit(ctx context.Context, a state.Agent) (*gitConfigSection, error) {
	if !a.OnCluster() {
		return nil, nil
	}
	p, err := m.projectGitProfile(ctx, a.Project)
	if err != nil {
		return nil, err
	}
	section, err := m.deliverGitProfile(ctx, a, p)
	if err != nil {
		return nil, err
	}
	if err := m.clonePodGit(ctx, a, p, section); err != nil {
		return nil, err
	}
	return section, nil
}

// clonePodGit gives a pod with no repository yet one from its profile. The
// volume is the truth for everything else, so a tree whose HEAD resolves is
// left exactly as it is: only a first start clones.
func (m *Manager) clonePodGit(ctx context.Context, a state.Agent, p *gitProfile, section *gitConfigSection) error {
	if err := m.clonePodGitSteps(ctx, a, p, section); err != nil {
		return podGitError(p, err)
	}
	return nil
}

func (m *Manager) clonePodGitSteps(ctx context.Context, a state.Agent, p *gitProfile, section *gitConfigSection) error {
	dir := m.Worktree(a)
	user := m.machineUser(a).Name
	head := "git -C " + shellQuote(dir) + " rev-parse --verify HEAD"
	if err := m.machine(a).UserExec(ctx, user, head, nil, nil, nil); err == nil {
		return nil
	}
	var config []string
	switch {
	case section.SSHCommand != "":
		config = []string{"-c", shellQuote("core.sshCommand=" + section.SSHCommand)}
	case section.Helper != "":
		config = []string{"-c", "credential.helper=" + shellQuote(section.Helper)}
	}
	clone := "cd " + shellQuote(dir) + " && GIT_TERMINAL_PROMPT=0 git " + strings.Join(config, " ") + " clone --quiet " + shellQuote(p.URL) + " ."
	if err := m.runPodGit(ctx, a, user, clone); err != nil {
		return fmt.Errorf("%s's pod could not clone the %s git profile: %w", a.Ref(), p.Profile, err)
	}
	// The branch starts at the commit the master resolved on its own checkout,
	// so the pod and the master see the same base; that commit has to be one the
	// profile's remote has, which a clone of another branch may not include.
	base := a.BaseCommit
	if base == "" {
		base = a.BaseRef
	}
	if base == "" {
		return fmt.Errorf("%s's pod cloned the %s git profile, and has no base commit to start %s from", a.Ref(), p.Profile, a.Branch)
	}
	exists := "git -C " + shellQuote(dir) + " cat-file -e " + shellQuote(base+"^{commit}")
	if err := m.runPodGit(ctx, a, user, exists); err != nil {
		return fmt.Errorf("%s's pod cloned the %s git profile, and the base commit %s is not in it: the base commit must exist on the remote profile (%s)", a.Ref(), p.Profile, base, p.URL)
	}
	checkout := "git -C " + shellQuote(dir) + " checkout -B " + shellQuote(a.Branch) + " " + shellQuote(base)
	if err := m.runPodGit(ctx, a, user, checkout); err != nil {
		return fmt.Errorf("%s's pod could not start %s from %s: %w", a.Ref(), a.Branch, base, err)
	}
	return nil
}

// checkoutPodFork moves a fork's worktree onto its own branch, at the commit
// its snapshot was taken on. The claim was unpacked from the source's archive,
// so the tree already holds the snapshot's files, uncommitted and untracked ones
// included; moving the branch pointer keeps them, which is what makes the copy a
// working tree rather than a fresh clone.
func (m *Manager) checkoutPodFork(ctx context.Context, a state.Agent) error {
	base := a.BaseCommit
	if base == "" {
		base = "HEAD"
	}
	command := "git -C " + shellQuote(m.Worktree(a)) + " checkout -B " + shellQuote(a.Branch) + " " + shellQuote(base)
	if err := m.runPodGit(ctx, a, m.machineUser(a).Name, command); err != nil {
		return fmt.Errorf("%s's pod could not start %s at %s: %w", a.Ref(), a.Branch, base, err)
	}
	return nil
}

// runPodGit runs one git command as the pod's agent and keeps what it said on
// stderr, which is where git explains a clone that didn't work. The caller
// redacts before the error leaves this file.
func (m *Manager) runPodGit(ctx context.Context, a state.Agent, user, command string) error {
	var stderr strings.Builder
	err := m.machine(a).UserExec(ctx, user, command, nil, nil, &stderr)
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}

// podGitError removes every form of the profile's secret from an error before
// anything logs or shows it.
func podGitError(p *gitProfile, err error) error {
	if err == nil {
		return nil
	}
	return errors.New(gitcred.Redact(err.Error(), p.secret()))
}

// gitConfigFor builds an agent's ~/.gitconfig, with the profile's login for a
// pod. pod is nil for an agent whose remote is this host's checkout.
func gitConfigFor(helper, hooksPath string, pod *gitConfigSection) string {
	var b strings.Builder
	if name, email := gitIdentity(); name != "" && email != "" {
		fmt.Fprintf(&b, "[user]\n\tname = %s\n\temail = %s\n", gitQuote(name), gitQuote(email))
	}
	if hooksPath != "" {
		fmt.Fprintf(&b, "[core]\n\thooksPath = %s\n", gitQuote(hooksPath))
	}
	if helper != "" {
		fmt.Fprintf(&b, `[credential "https://github.com"]
	helper =
	helper = %s
`, helper)
		// An ssh profile keeps git's remotes on ssh: rewriting github.com's to
		// https would turn the profile's own login into a password prompt.
		if pod == nil || pod.SSHCommand == "" {
			fmt.Fprintf(&b, `[url "https://github.com/"]
	insteadOf = git@github.com:
	insteadOf = ssh://git@github.com/
`)
		}
	}
	if pod != nil {
		switch {
		case pod.SSHCommand != "":
			fmt.Fprintf(&b, "[core]\n\tsshCommand = %s\n", pod.SSHCommand)
		case pod.Helper != "":
			fmt.Fprintf(&b, "[credential %s]\n\thelper =\n\thelper = %s\n", gitQuote("https://"+pod.Host), gitQuote(pod.Helper))
		}
	}
	return b.String()
}
