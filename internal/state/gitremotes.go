package state

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strings"

	"agentbox/internal/trace"
)

// A project's agents clone and push with one of two remotes, the profiles
// below: private is the server their branches stay on while they work, public
// is where a finished change is published. Each profile is a URL, how that URL
// signs in, and which stored login signs it, so a pod never has to guess and
// never has to hold the user's own identity.
const (
	// GitProfilePrivate is where work-in-progress branches go: the private
	// Forgejo here, and never GitHub.
	GitProfilePrivate = "private"
	// GitProfilePublic is where a finished change is published: the GitHub
	// remote, which agent branches reach only when the user says so.
	GitProfilePublic = "public"
)

// How a profile signs in to its URL.
const (
	// GitAuthToken is an HTTPS token, delivered into the pod and answered for
	// git by the helper internal/gitcred builds.
	GitAuthToken = "token"
	// GitAuthSSHKey is an SSH key the pod keeps in its tmpfs for the length of
	// its run.
	GitAuthSSHKey = "ssh-key"
	// GitAuthGitHubAccount is a stored GitHub account (internal/credentials):
	// the same login a host agent's git and `gh` already use.
	GitAuthGitHubAccount = "github-account"
)

// gitAlias is the scheme a project may store in place of a real URL:
// forge:h4ad/agentbox.git, which this machine's git turns into an https or ssh
// URL through url.<base>.insteadOf (internal/gitcred). The pod is given the
// resolved URL, so the alias never has to exist in its image.
const gitAlias = "forge:"

// GitRemote is one of a project's two git profiles: where its agents clone
// from, and how they sign in there.
type GitRemote struct {
	Project string
	// Profile is GitProfilePrivate or GitProfilePublic.
	Profile string
	// URL is where the remote is: an http, https, ssh or git URL, git's own
	// ssh shorthand with a user (git@hub:owner/repo.git), or a forge: alias
	// this machine resolves before a pod sees it.
	URL string
	// Auth is GitAuthToken, GitAuthSSHKey or GitAuthGitHubAccount.
	Auth string
	// Credential names the login the profile signs in with
	// (internal/credentials): the token or key for the first two kinds, the
	// GitHub account for the third. Empty is a remote nobody signs in to,
	// which only a public one can be.
	Credential string
}

// SetProjectGitRemote records one profile, replacing it. A project has at most
// one private and one public remote, so setting a profile again is how its URL
// or its login changes.
func (s *Store) SetProjectGitRemote(ctx context.Context, r GitRemote) error {
	p, err := s.Project(ctx, r.Project)
	if err != nil {
		return err
	}
	// Stored against the slug, whatever the caller called the project: the row
	// has to survive a change to what the project is called, and RemoveProject
	// deletes by slug.
	r.Project = p.Name
	if err := checkGitRemote(r); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO project_git_remotes (project, profile, url, auth, credential)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project, profile) DO UPDATE SET url = excluded.url, auth = excluded.auth,
			credential = excluded.credential`,
		r.Project, r.Profile, r.URL, r.Auth, r.Credential)
	return err
}

// ProjectGitRemotes is the profiles a project has, private first. A project
// with neither can't have agents in a cluster, which the create path checks
// before it makes a pod and says which profile is missing.
func (s *Store) ProjectGitRemotes(ctx context.Context, project string) ([]GitRemote, error) {
	p, err := s.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT profile, url, auth, credential FROM project_git_remotes
		WHERE project = ? ORDER BY CASE profile WHEN ? THEN 0 ELSE 1 END, profile`, p.Name, GitProfilePrivate)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var remotes []GitRemote
	for rows.Next() {
		r := GitRemote{Project: p.Name}
		if err := rows.Scan(&r.Profile, &r.URL, &r.Auth, &r.Credential); err != nil {
			return nil, err
		}
		remotes = append(remotes, r)
	}
	return remotes, rows.Err()
}

// RemoveProjectGitRemote forgets one profile. Removing the one the project's
// agents use moves them to the other profile when the project still has it,
// and back to private when it has none, so that projects.agent_remote never
// names a profile that is missing while another one is there.
func (s *Store) RemoveProjectGitRemote(ctx context.Context, project, profile string) error {
	p, err := s.Project(ctx, project)
	if err != nil {
		return err
	}
	if err := checkGitProfile(profile); err != nil {
		return fmt.Errorf("project %q: %w", p.Name, err)
	}
	// One transaction: the profile and what the agents use go together, so a
	// failure can't leave a project pointing at a remote it just forgot.
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM project_git_remotes WHERE project = ? AND profile = ?`, p.Name, profile)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q has no %s git profile: %w", p.Name, profile, ErrNotFound)
	}
	var agentRemote string
	if err := tx.QueryRowContext(ctx, `SELECT `+projectAgentRemote+` FROM projects WHERE name = ?`, p.Name).Scan(&agentRemote); err != nil {
		return err
	}
	if agentRemote != profile {
		return tx.Commit()
	}
	other := otherGitProfile(profile)
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_git_remotes WHERE project = ? AND profile = ?`, p.Name, other).Scan(&left); err != nil {
		return err
	}
	if left == 0 {
		// Nothing is left to point at: the default, which the create path
		// reads as "there is no profile to start a pod with".
		other = GitProfilePrivate
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET agent_remote = ? WHERE name = ?`, other, p.Name); err != nil {
		return err
	}
	return tx.Commit()
}

// SetProjectAgentRemote picks which profile a project's agents use. A profile
// the project doesn't have is refused, so a mistake is answered by the command
// that made it rather than by a pod that can't clone.
func (s *Store) SetProjectAgentRemote(ctx context.Context, project, profile string) error {
	p, err := s.Project(ctx, project)
	if err != nil {
		return err
	}
	if err := checkGitProfile(profile); err != nil {
		return fmt.Errorf("project %q: %w", p.Name, err)
	}
	var have int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_git_remotes WHERE project = ? AND profile = ?`,
		p.Name, profile).Scan(&have); err != nil {
		return err
	}
	if have == 0 {
		return fmt.Errorf("project %q has no %s git profile: set it first with `agentbox project git %s set %s`",
			p.Name, profile, p.Name, profile)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET agent_remote = ? WHERE name = ?`, profile, p.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", p.Name, ErrNotFound)
	}
	return nil
}

// agentRemoteOrPrivate is the profile a project's agents use when the column
// says nothing: rows written before it existed, and a project whose profiles
// were all removed. Private is the safe one to fall back to.
func agentRemoteOrPrivate(profile string) string {
	if profile == "" {
		return GitProfilePrivate
	}
	return profile
}

func checkGitProfile(profile string) error {
	switch profile {
	case GitProfilePrivate, GitProfilePublic:
		return nil
	}
	return fmt.Errorf("git profile %q: use %s or %s", profile, GitProfilePrivate, GitProfilePublic)
}

// otherGitProfile is the profile a project has besides this one.
func otherGitProfile(profile string) string {
	if profile == GitProfilePrivate {
		return GitProfilePublic
	}
	return GitProfilePrivate
}

// checkGitRemote refuses what no pod could clone from: a profile that isn't
// one, a way of signing in that doesn't exist, a remote without a URL, or a
// login written into the URL where a credential belongs.
func checkGitRemote(r GitRemote) error {
	if err := checkGitProfile(r.Profile); err != nil {
		return fmt.Errorf("project %q: %w", r.Project, err)
	}
	switch r.Auth {
	case GitAuthToken, GitAuthSSHKey, GitAuthGitHubAccount:
	default:
		return fmt.Errorf("project %q's %s git profile: auth %q: use %s, %s or %s",
			r.Project, r.Profile, r.Auth, GitAuthToken, GitAuthSSHKey, GitAuthGitHubAccount)
	}
	if r.Auth == GitAuthGitHubAccount && r.Credential == "" {
		return fmt.Errorf("project %q's %s git profile signs in with a GitHub account, so it needs the account's name",
			r.Project, r.Profile)
	}
	if strings.ContainsAny(r.Credential, " \t\r\n") {
		return fmt.Errorf("project %q's %s git profile: credential %q: name one stored login, with no spaces",
			r.Project, r.Profile, r.Credential)
	}
	if err := checkGitURL(r.URL); err != nil {
		return fmt.Errorf("project %q's %s git profile: %w", r.Project, r.Profile, err)
	}
	// An alias is checked once it is resolved (agent.ResolveGitProfileURL);
	// a URL that names its host is refused here, before anything is stored.
	if r.Auth == GitAuthGitHubAccount && !strings.HasPrefix(strings.TrimSpace(r.URL), gitAlias) {
		if host := GitHostname(r.URL); host != GitHubHost {
			return fmt.Errorf("project %q's %s git profile signs in as GitHub account %q, whose token is GitHub's alone, and %s is on %s: use --auth token with a login for that host",
				r.Project, r.Profile, r.Credential, r.URL, host)
		}
	}
	return nil
}

// GitHubHost is the one host a GitHub account's token may be sent to.
const GitHubHost = "github.com"

// GitHostname is the host a git URL or git's ssh shorthand names, without the
// user or the port and in lower case: github.com for both
// https://github.com/o/r.git and git@github.com:o/r.git. Empty when raw names
// none, such as a local path or a forge: alias.
func GitHostname(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := neturl.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	if strings.HasPrefix(raw, gitAlias) {
		return ""
	}
	host, _, ok := strings.Cut(raw, ":")
	if !ok || strings.ContainsAny(host, "/ \\") {
		return ""
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return strings.ToLower(host)
}

// checkGitURL refuses a remote that isn't one. A project may store an alias,
// because the pod never sees it: internal/gitcred turns it into the real URL
// on this machine.
func checkGitURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("it needs the URL its agents clone from")
	}
	if rest, ok := strings.CutPrefix(raw, gitAlias); ok {
		if rest == "" || strings.ContainsAny(rest, " \t") {
			return fmt.Errorf("%q names no repository: write %sowner/repo.git, with no spaces", raw, gitAlias)
		}
		return nil
	}
	if strings.Contains(raw, "://") {
		u, err := neturl.Parse(raw)
		if err != nil {
			return fmt.Errorf("%q: %w", raw, err)
		}
		switch u.Scheme {
		case "http", "https", "ssh", "git", "git+ssh":
		default:
			return fmt.Errorf("%q: use an http, https, ssh or git URL, or a %s alias", raw, gitAlias)
		}
		if u.Host == "" {
			return fmt.Errorf("%q has no host", raw)
		}
		if u.User != nil {
			if _, set := u.User.Password(); set {
				return fmt.Errorf("%s carries a password: the login belongs in the profile's credential, not in its URL", u.Redacted())
			}
		}
		return nil
	}
	// git's own ssh shorthand, which names the user: git@hub:owner/repo.git.
	if user, path, ok := strings.Cut(raw, ":"); ok && path != "" &&
		strings.Contains(user, "@") && !strings.ContainsAny(user, "/ \t") {
		return nil
	}
	return fmt.Errorf("%q: use an absolute URL like https://host/owner/repo.git, git@host:owner/repo.git, or a %s alias", raw, gitAlias)
}
