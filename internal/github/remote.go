package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNoRepo means the project's git remote isn't a GitHub repository. The two
// ways that happens are worth telling apart, and both wrap it: ErrNoOrigin
// when the checkout has no origin at all, and NotGitHubError when it has one
// that points somewhere else. Saying the wrong one sends people looking in the
// wrong place.
var ErrNoRepo = errors.New("this project has no GitHub remote")

// ErrNoOrigin means the checkout has no origin remote at all.
var ErrNoOrigin = errors.New("no origin remote")

// NotGitHubError is an origin remote that doesn't resolve to a GitHub
// repository. It carries the remote itself, with any credentials redacted, so
// what the user is told names the URL instead of claiming there is none.
type NotGitHubError struct{ Remote string }

func (e *NotGitHubError) Error() string {
	return fmt.Sprintf("origin %s is not a GitHub repository", e.Remote)
}

func (e *NotGitHubError) Unwrap() error { return ErrNoRepo }

// githubHost is the only host AgentBox reads pull requests from. GitHub
// Enterprise is out of scope: the API base URL would have to move with it, and
// a token per host (decision D57).
const githubHost = "github.com"

// The URL forms git writes. A remote with a scheme keeps its host after any
// embedded credentials and before any port; the scp-like form has no scheme
// and separates host from path with a colon. repoPath is the owner/name tail
// both end in, with or without .git.
var (
	remoteWithScheme = regexp.MustCompile(`^(?:https|ssh|git)://(?:[^/@]*@)?([^/:]+)(?::\d+)?/(.+)$`)
	remoteSCPLike    = regexp.MustCompile(`^(?:[^/@]*@)?([^/:]+):(.+)$`)
	repoPath         = regexp.MustCompile(`^([^/]+)/(.+?)(?:\.git)?/?$`)
)

// splitRemote pulls the host and the owner/name out of a git remote URL,
// without deciding whether the host is GitHub.
func splitRemote(remote string) (host string, repo Repo, ok bool) {
	m := remoteWithScheme.FindStringSubmatch(remote)
	if m == nil {
		m = remoteSCPLike.FindStringSubmatch(remote)
	}
	if m == nil {
		return "", Repo{}, false
	}
	path := repoPath.FindStringSubmatch(strings.TrimPrefix(m[2], "/"))
	if path == nil {
		return "", Repo{}, false
	}
	return m[1], Repo{Owner: path[1], Name: path[2]}, true
}

// ParseRemote reads owner and name out of a git remote URL.
//
// A host that isn't github.com is resolved through SSH configuration first,
// the way git's own ssh does: `git@github.com-work:acme/pawly.git` is a
// GitHub remote when `Host github.com-work` in ~/.ssh/config points at
// github.com, which is the standard way to keep two GitHub accounts on two
// keys. A host that resolves anywhere else is genuinely not GitHub.
func ParseRemote(remote string) (Repo, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return Repo{}, fmt.Errorf("%w: %w", ErrNoRepo, ErrNoOrigin)
	}
	host, repo, ok := splitRemote(remote)
	if !ok {
		return Repo{}, &NotGitHubError{Remote: Redact(remote)}
	}
	if !strings.EqualFold(host, githubHost) && !strings.EqualFold(resolvedHost(host), githubHost) {
		return Repo{}, &NotGitHubError{Remote: Redact(remote)}
	}
	return repo, nil
}

// RepoOf finds the GitHub repository a checkout pushes to, from its origin.
func RepoOf(root string) (Repo, error) {
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return Repo{}, fmt.Errorf("%w: %w", ErrNoRepo, ErrNoOrigin)
	}
	return ParseRemote(string(out))
}

// credentialsPattern matches the password half of a URL's embedded
// credentials, `user:password@`, which is where a token hides. `git@host`
// has no password and is left alone.
var credentialsPattern = regexp.MustCompile(`([^:/@]+):[^@/]*@`)

// Redact hides the password half of a remote's embedded credentials, so a
// token in an origin URL never reaches a response, a log or an error. There
// is at least one real user whose origin is
// `https://x-access-token:<token>@github.com/owner/repo.git`.
func Redact(remote string) string {
	return credentialsPattern.ReplaceAllString(remote, "$1:***@")
}

// resolvedHosts remembers what `ssh -G` said each host resolves to, for the
// daemon's lifetime. `ssh -G` is cheap, but the fleet and the Pull requests
// tab both poll, so this runs often. SSH configuration doesn't change under a
// running daemon often enough to be worth expiring.
var resolvedHosts sync.Map // host -> hostname ssh -G reported

// resolvedHost is the real hostname an SSH host name points at, reading SSH
// configuration the way git does rather than parsing ~/.ssh/config here: the
// file has Include, Match and per-user and system-wide copies, and `ssh -G`
// is the only thing that gets all of that right. A host that is not an alias
// resolves to itself, which is also what a missing or failing ssh gives, so
// the old behaviour is what AgentBox falls back to.
func resolvedHost(host string) string {
	if cached, ok := resolvedHosts.Load(host); ok {
		return cached.(string)
	}
	real := askSSH(host)
	resolvedHosts.Store(host, real)
	return real
}

func askSSH(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-G", host).Output()
	if err != nil {
		return host
	}
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), " "); ok && key == "hostname" {
			return strings.TrimSpace(value)
		}
	}
	return host
}
