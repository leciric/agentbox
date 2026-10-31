package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// ErrCloneDestExists is Clone refusing a folder that is already there, which
// the caller may offer to add as it is instead.
var ErrCloneDestExists = errors.New("the folder already exists")

// CloneTimeout is how long one clone may take.
const CloneTimeout = 15 * time.Minute

// Clone clones a repository from a URL into dest, for a project being added
// from a link: the app's and the CLI's, the Home chat's add_project and
// another machine's lead (the daemon's cloneRepo). A repository on github.com
// is cloned first with the GitHub account the agents would get (account, or
// the machine's default when it is ""), over https even for a git@ link, so a
// private one needs no other login; when that fails, or there is no account,
// or the host isn't GitHub, with this machine's own git credentials (its
// credential helpers, gh's among them, and ssh keys). Progress goes to log a
// line at a time. dest must not exist yet; a clone that fails leaves nothing
// behind.
func (m *Manager) Clone(ctx context.Context, url, dest, account string, log io.Writer) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists: add it as it is, or clone into another folder: %w", dest, ErrCloneDestExists)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, CloneTimeout)
	defer cancel()
	var token string
	if onGitHub(url) {
		if name, err := m.GitHubAccountFor(state.Project{}, account); err != nil {
			return err
		} else if name != "" {
			if token, err = m.Creds.GitHubToken(name); err != nil {
				m.logf("Cloning %s: the GitHub account %s has no token: %v", url, name, err)
				token = ""
			}
			account = name
		}
	}
	m.logf("Cloning %s into %s", url, dest)
	if token != "" {
		_, _ = fmt.Fprintf(log, "==> Cloning %s with the GitHub account %s\n", url, account)
		out, err := runClone(ctx, url, dest, token, log)
		if err == nil {
			return nil
		}
		_ = os.RemoveAll(dest)
		if ctx.Err() != nil {
			return cloneFailure(url, "", out, ctx.Err())
		}
		_, _ = fmt.Fprintf(log, "==> The GitHub account %s couldn't clone it (%s); trying this machine's own git credentials\n", account, gitSaid(out))
	} else {
		account = ""
		_, _ = fmt.Fprintf(log, "==> Cloning %s with this machine's git credentials\n", url)
	}
	out, err := runClone(ctx, url, dest, "", log)
	if err != nil {
		_ = os.RemoveAll(dest)
		return cloneFailure(url, account, out, err)
	}
	return nil
}

// runClone runs one git clone, with githubToken for github.com in place of
// the user's credential helpers for it when it is set (as gitrepo.Fetch
// does), and answers what git said other than its progress.
func runClone(ctx context.Context, url, dest, githubToken string, log io.Writer) (string, error) {
	var args []string
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	if githubToken != "" {
		// An empty helper clears the ones configured before it, since -c is
		// read last; the git@ forms go over https, where the token works.
		key := "credential.https://github.com.helper="
		args = append(args, "-c", key, "-c", key+gitrepo.GitHubCredentialHelper,
			"-c", "url.https://github.com/.insteadOf=git@github.com:",
			"-c", "url.https://github.com/.insteadOf=ssh://git@github.com/")
		env = append(env, "GH_TOKEN="+githubToken)
	}
	args = append(args, "clone", "--progress", "--", url, dest)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	// ssh, started by git, holds the output open after git is killed.
	cmd.WaitDelay = 2 * time.Second
	p := &cloneProgress{log: log}
	cmd.Stdout, cmd.Stderr = p, p
	err := cmd.Run()
	p.flush()
	return strings.TrimSpace(p.said.String()), err
}

// onGitHub is whether a repository URL is on github.com, in any of the forms
// git takes: https://, ssh://, git@github.com:owner/repo.
func onGitHub(url string) bool {
	u := strings.ToLower(url)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if at := strings.Index(u, "@"); at >= 0 && at < strings.IndexAny(u+"/", "/:") {
		u = u[at+1:]
	}
	host, _, _ := strings.Cut(u, "/")
	host, _, _ = strings.Cut(host, ":")
	return host == "github.com" || host == "www.github.com"
}

// cloneFailure words a clone git refused: no access (the commonest, and the
// one with something to do about it) or a host it couldn't reach, with git's
// own last line; anything else is git's words.
func cloneFailure(url, account, out string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("cloning %s took longer than %s: try again, or clone it yourself and add the folder", url, CloneTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("cloning %s: %w", url, err)
	}
	low := strings.ToLower(out)
	said := gitSaid(out)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("repository not found", "authentication failed", "could not read username", "could not read password",
		"permission denied", "terminal prompts disabled", "access denied", "returned error: 403", "returned error: 401",
		"not authorized", "could not read from remote repository"):
		who := "this machine's git credentials"
		if account != "" {
			who = fmt.Sprintf("the GitHub account %s and this machine's git credentials", account)
		}
		return fmt.Errorf("can't clone %s: it doesn't exist, or %s have no access to it (%s). Check the link, or add a GitHub account that can see it (Settings, or agentbox auth github)", url, who, said)
	case has("could not resolve host", "connection timed out", "connection refused", "network is unreachable"):
		return fmt.Errorf("can't reach the host of %s (%s): check the link and this machine's connection", url, said)
	}
	if said == "" {
		return fmt.Errorf("cloning %s: %w", url, err)
	}
	return fmt.Errorf("cloning %s: %s", url, said)
}

// gitSaid is the last thing git said, which is what went wrong.
func gitSaid(out string) string {
	return strings.TrimPrefix(lastLine(out), "fatal: ")
}

// cloneProgress passes git clone's --progress on to a job's log, which keeps
// whole lines: git redraws "Receiving objects:  42% (…)" with \r hundreds of
// times. What the server does (counting, compressing) keeps only its last
// line, and what this machine does (receiving, resolving, checking out) a
// line every quarter of the way too. Everything else git says is kept in said
// too, for its error.
type cloneProgress struct {
	log     io.Writer
	partial []byte
	phase   string
	percent int
	done    string // the phase whose last line was kept
	said    bytes.Buffer
}

var progressLine = regexp.MustCompile(`^(remote: )?([A-Za-z ]+):\s+(\d+)%`)

func (p *cloneProgress) Write(b []byte) (int, error) {
	p.partial = append(p.partial, b...)
	for {
		i := bytes.IndexAny(p.partial, "\r\n")
		if i < 0 {
			return len(b), nil
		}
		p.line(string(p.partial[:i]))
		p.partial = p.partial[i+1:]
	}
}

func (p *cloneProgress) flush() {
	if len(p.partial) > 0 {
		p.line(string(p.partial))
		p.partial = nil
	}
}

func (p *cloneProgress) line(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if m := progressLine.FindStringSubmatch(s); m != nil {
		remote, phase := m[1] != "", m[2]
		percent, _ := strconv.Atoi(m[3])
		if phase != p.phase {
			p.phase, p.percent = phase, 0
		}
		switch {
		case strings.HasSuffix(s, "done.") || strings.Contains(s, ", done"):
			if p.done == phase {
				return
			}
			p.done = phase
		case remote || percent < p.percent+25 || percent == 100:
			return
		default:
			p.percent = percent
		}
		_, _ = fmt.Fprintln(p.log, s)
		return
	}
	p.said.WriteString(s + "\n")
	_, _ = fmt.Fprintln(p.log, s)
}
