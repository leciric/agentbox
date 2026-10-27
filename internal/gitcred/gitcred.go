// Package gitcred turns a project's stored git remote into what an agent pod
// can clone and push with, and keeps the login it uses out of the pod's
// process list and out of anything written down.
//
// A project may store an alias rather than a URL (forge:h4ad/agentbox.git).
// That alias, the url.<base>.insteadOf rules behind it and the short host
// names in ~/.ssh/config exist only on this machine, and the agent image
// should not have to carry any of them, so everything is resolved here and the
// pod is handed a real URL.
package gitcred

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	neturl "net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// forgeAlias is the scheme a project's URL may use instead of a real one.
const forgeAlias = "forge:"

// Config is where this machine keeps the two files that decide what an alias
// or a short name means.
type Config struct {
	// GitConfig is the git config file to read url.<base>.insteadOf rules
	// from. Empty is this user's own global file, ~/.gitconfig or
	// $XDG_CONFIG_HOME/git/config, which is what a real run reads.
	GitConfig string
	// SSHConfig is the ssh client config to read host names from. Empty is
	// this user's ~/.ssh/config.
	SSHConfig string
}

// Resolve turns what a project stores into the URL a pod can clone: no forge:
// alias, no ssh short name, and no user identity beyond what the URL needs.
//
// The rules run in the order git itself applies them on this machine: the
// url.<base>.insteadOf rewrites (the longest matching prefix wins, and a
// forge: alias is only ever one of those), then the ssh client config for an
// ssh URL or git's own user@host:path shorthand. A name no Host block mentions
// is left as it is: it may resolve through DNS.
//
// A URL with a password in it is refused rather than resolved, at either end
// of the rewriting: a password in a URL is copied into every pod that clones
// with it and into every log line that names the remote, and a credential
// handed to git as a helper is what the profile already stores.
func Resolve(ctx context.Context, raw string, cfg Config) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("no git remote to resolve")
	}
	if err := refusePassword(raw); err != nil {
		return "", err
	}
	rewrites, err := insteadOf(ctx, cfg.GitConfig)
	if err != nil {
		return "", err
	}
	resolved, matched := rewriteURL(raw, rewrites)
	if !matched && strings.HasPrefix(raw, forgeAlias) {
		return "", fmt.Errorf("nothing on this machine turns %s into a real URL: set the rule once, then it works for every project, with\n"+
			"\tgit config --global url.<https base>.insteadof %s", raw, forgeAlias)
	}
	if strings.HasPrefix(resolved, forgeAlias) {
		// A rule whose base is another alias leaves the pod a name only this
		// machine's git understands, and git doesn't rewrite twice either.
		return "", fmt.Errorf("%s resolves to %s, which is still a %s alias: point the rule at the real URL", raw, resolved, forgeAlias)
	}
	if resolved, err = resolveSSH(resolved, cfg.SSHConfig); err != nil {
		return "", err
	}
	// A rewrite's base may carry a password of its own.
	if err := refusePassword(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// CredentialHelper is the content of the file a pod writes under /run/agentbox
// and git is pointed at for host, with
//
//	git config credential.helper '!sh /run/agentbox/<name>'
//
// so that `get` is answered with this username and this token, and `store` and
// `erase` are answered with nothing: a token git was handed is not written
// back anywhere.
//
// The file is mode 0600, holds the token as a shell variable, and lives in the
// pod's tmpfs, so it goes away when the pod stops. Its content must never be
// configured as a bare "!" helper: git puts such a helper's whole text on the
// shell's command line, where the token would sit in the pod's process list.
// Run by path, with only the file's name in that command line, it can't be
// read there.
func CredentialHelper(host, username, token string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n")
	fmt.Fprintf(&b, "# git credential helper for %s, written by AgentBox in this pod while it runs.\n", oneLine(host))
	b.WriteString("# The token below is a shell variable, never an argument to a command, so it\n")
	b.WriteString("# stays out of the process list; git runs this file through sh, by name.\n")
	fmt.Fprintf(&b, "username=%s\n", shellQuote(username))
	fmt.Fprintf(&b, "password=%s\n", shellQuote(token))
	b.WriteString("case \"$1\" in\n")
	b.WriteString("get) printf 'username=%s\\npassword=%s\\n' \"$username\" \"$password\" ;;\n")
	b.WriteString("store | erase) ;;\n")
	b.WriteString("esac\n")
	return b.String()
}

// SSHCommand is what GIT_SSH_COMMAND is set to in a pod whose key is keyFile:
// ssh with that key and no others, no password prompt and a first connection
// that records the host key it found, so a clone fails with ssh's reason
// instead of waiting for a terminal nobody is watching.
func SSHCommand(keyFile string) string {
	return "ssh -i " + shellQuote(keyFile) +
		" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new"
}

// Redact removes every secret from text before it is logged, stored or shown:
// a failed clone's stderr, a job log, an error message. Each secret becomes
// ***, and so does its URL-escaped form, which is how a token shows up inside
// a URL or in a message that echoes one.
func Redact(text string, secrets ...string) string {
	var ordered []string
	for _, secret := range secrets {
		if secret != "" && !slices.Contains(ordered, secret) {
			ordered = append(ordered, secret)
		}
	}
	// Longest first, so a secret that contains another is removed whole
	// rather than in two pieces.
	slices.SortStableFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	for _, secret := range ordered {
		forms := []string{secret}
		for _, escaped := range []string{neturl.QueryEscape(secret), neturl.PathEscape(secret)} {
			if escaped != secret && !slices.Contains(forms, escaped) {
				forms = append(forms, escaped)
			}
		}
		for _, form := range forms {
			text = strings.ReplaceAll(text, form, "***")
		}
	}
	return text
}

// rewrite is one url.<base>.insteadOf: from is a prefix a URL may start with,
// to the base that replaces it.
type rewrite struct {
	from, to string
}

// insteadOf reads the URL rewrites from git itself rather than parsing the
// config file, so an include, a conditional or a value's escaping behaves
// exactly as it does in the user's own checkout. An explicit file is read as
// the whole global config, with no system or repository config to change the
// answer, which is what lets a test use a file of its own.
func insteadOf(ctx context.Context, gitConfig string) ([]rewrite, error) {
	// --global, so that which file is read doesn't depend on the repository
	// the daemon happens to be in, and a local url.*.insteadOf never decides
	// what an agent clones from.
	args := []string{"config", "--global", "--null", "--get-regexp", `^url\..*\.insteadof$`}
	cmd := exec.CommandContext(ctx, "git", args...)
	if gitConfig != "" {
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"GIT_CONFIG_GLOBAL=" + gitConfig,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
		}
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// git exits 1 with nothing printed when no key matches, which is a
		// machine that has never set an alias.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 && stderr.Len() == 0 {
			return nil, nil
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("reading url.*.insteadOf from git config: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("reading url.*.insteadOf from git config: %w", err)
	}
	return parseInsteadOf(string(out)), nil
}

// parseInsteadOf parses the records git prints for --null --get-regexp: a key,
// a newline, the value, and a NUL ending each one.
func parseInsteadOf(out string) []rewrite {
	var rewrites []rewrite
	for _, record := range strings.Split(out, "\x00") {
		key, value, ok := strings.Cut(record, "\n")
		if !ok || value == "" {
			// An empty insteadOf would match every URL, which is nobody's
			// intent.
			continue
		}
		base, ok := strings.CutPrefix(key, "url.")
		if !ok {
			continue
		}
		if base, ok = strings.CutSuffix(base, ".insteadof"); !ok || base == "" {
			continue
		}
		rewrites = append(rewrites, rewrite{from: value, to: base})
	}
	return rewrites
}

// rewriteURL is git's url.<base>.insteadOf: the longest prefix that matches is
// replaced by its base, as a plain string join, because that is what git does.
// A tie goes to whichever rule git read first.
func rewriteURL(raw string, rewrites []rewrite) (string, bool) {
	var best rewrite
	found := false
	for _, r := range rewrites {
		if !strings.HasPrefix(raw, r.from) {
			continue
		}
		if !found || len(r.from) > len(best.from) {
			best, found = r, true
		}
	}
	if !found {
		return raw, false
	}
	return best.to + raw[len(best.from):], true
}

// refusePassword fails on a URL that carries a password.
func refusePassword(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil || u.User == nil {
		return nil
	}
	if _, set := u.User.Password(); set {
		return fmt.Errorf("the git remote %s carries a password: store it as the profile's credential instead, and the pod gets it as a helper rather than in its URL", u.Redacted())
	}
	return nil
}

// sshBlock is one Host block of the ssh client config, with the first value
// each keyword was given in it.
type sshBlock struct {
	patterns []string
	hostName string
	user     string
	port     string
}

// sshHost is what the config says about a name: where to connect, and the user
// and port to use when the URL names none.
type sshHost struct {
	hostName, user, port string
}

// resolveSSH replaces a short host name with what the ssh client config says
// about it, for the two forms git accepts: an ssh:// URL and its own
// [user@]host:path shorthand. The shorthand comes back as an ssh:// URL,
// because that is the one form that can carry the port a block may name.
func resolveSSH(raw, sshConfig string) (string, error) {
	blocks, err := readSSHConfig(sshConfig)
	if err != nil || len(blocks) == 0 {
		return raw, err
	}
	if strings.Contains(raw, "://") {
		u, err := neturl.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("%q: %w", raw, err)
		}
		if u.Scheme != "ssh" && u.Scheme != "git+ssh" {
			return raw, nil
		}
		want := hostFor(blocks, u.Hostname())
		if want == (sshHost{}) {
			return raw, nil
		}
		user := ""
		if u.User != nil {
			user = u.User.Username()
		}
		if want.hostName == "" && want.port == "" && (want.user == "" || want.user == user) {
			// The block has nothing to say about this URL that it doesn't
			// already say itself.
			return raw, nil
		}
		host := u.Hostname()
		if want.hostName != "" {
			host = want.hostName
		}
		port := u.Port()
		if port == "" {
			port = want.port
		}
		if user == "" {
			user = want.user
		}
		switch {
		case port != "":
			u.Host = net.JoinHostPort(host, port)
		case strings.Contains(host, ":"):
			u.Host = "[" + host + "]" // a bare IPv6 address
		default:
			u.Host = host
		}
		u.User = nil
		if user != "" {
			u.User = neturl.User(user)
		}
		return u.String(), nil
	}

	// git's shorthand is host:path, with the user before an @ and no slash in
	// the host, which is what a Forgejo's clone button shows.
	host, repo, ok := strings.Cut(raw, ":")
	if !ok || repo == "" || host == "" || strings.ContainsAny(host, "/ \\") || strings.HasPrefix(raw, forgeAlias) {
		return raw, nil
	}
	user := ""
	if at := strings.LastIndex(host, "@"); at >= 0 {
		user, host = host[:at], host[at+1:]
	}
	want := hostFor(blocks, host)
	if want == (sshHost{}) || host == "" {
		return raw, nil
	}
	if want.hostName == "" && want.port == "" && (want.user == "" || want.user == user) {
		// Nothing to add that the shorthand doesn't already say.
		return raw, nil
	}
	if want.hostName != "" {
		host = want.hostName
	}
	if user == "" {
		user = want.user
	}
	var b strings.Builder
	b.WriteString("ssh://")
	if user != "" {
		b.WriteString(user)
		b.WriteString("@")
	}
	b.WriteString(host)
	if want.port != "" {
		b.WriteString(":")
		b.WriteString(want.port)
	}
	b.WriteString("/")
	b.WriteString(strings.TrimPrefix(repo, "/"))
	return b.String(), nil
}

// readSSHConfig reads the Host blocks of an ssh client config. A file that
// isn't there is no blocks at all, which leaves every short name as it is. An
// Include is not followed: what it would bring in has to be in this file.
func readSSHConfig(sshConfig string) ([]sshBlock, error) {
	if sshConfig == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return nil, nil
		}
		sshConfig = filepath.Join(home, ".ssh", "config")
	}
	content, err := os.ReadFile(sshConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var blocks []sshBlock
	current := -1
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(stripSSHComment(line))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		switch strings.ToLower(fields[0]) {
		case "host":
			blocks = append(blocks, sshBlock{patterns: fields[1:]})
			current = len(blocks) - 1
		case "match":
			// Match takes conditionals this package doesn't evaluate, so its
			// block is left to the next Host.
			current = -1
		case "hostname":
			if current >= 0 && blocks[current].hostName == "" && len(fields) > 1 {
				blocks[current].hostName = fields[1]
			}
		case "user":
			if current >= 0 && blocks[current].user == "" && len(fields) > 1 {
				blocks[current].user = fields[1]
			}
		case "port":
			if current >= 0 && blocks[current].port == "" && len(fields) > 1 {
				blocks[current].port = fields[1]
			}
		}
	}
	return blocks, nil
}

// stripSSHComment cuts what ssh reads as a comment: a # at the start of a line
// or after a blank, so a value that contains one is kept.
func stripSSHComment(line string) string {
	for i := 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	if strings.HasPrefix(line, "#") {
		return ""
	}
	return line
}

// hostFor is what the config says about a name: for each keyword, the first
// value any matching block gives it, which is how ssh itself reads them.
func hostFor(blocks []sshBlock, name string) sshHost {
	var want sshHost
	for _, block := range blocks {
		if !blockMatches(block.patterns, name) {
			continue
		}
		if want.hostName == "" {
			want.hostName = block.hostName
		}
		if want.user == "" {
			want.user = block.user
		}
		if want.port == "" {
			want.port = block.port
		}
	}
	return want
}

// blockMatches reports whether any of a Host line's patterns names this host.
// A pattern starting with ! excludes the block, as ssh's do, and the match
// ignores case, as host names do.
func blockMatches(patterns []string, name string) bool {
	matched := false
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
		if err != nil || !ok {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

// shellQuote quotes a value for a shell, so a token with a quote in it is
// still one word and nothing in it is expanded.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// oneLine keeps a value that goes into a comment from starting a line of its
// own.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
