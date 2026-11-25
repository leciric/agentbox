package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentbox/internal/hostos"
)

// SSH Host aliases. A remote like git@github-work:acme/app.git names a Host
// in its user's ~/.ssh/config, which says the real host (HostName) and the
// key to reach it with: the usual way to keep two GitHub accounts on two keys.
// Only that user's machine knows the alias, so what is compared with another
// machine's remotes, or sent there to clone, is the host it stands for.

// sshHosts remembers each host's answer for the process's lifetime: the
// fleet and the Pull requests tab read remotes on every poll, and SSH
// configuration doesn't change under a running daemon often enough to be
// worth expiring.
var sshHosts sync.Map // host -> its HostName

// SSHHostName is the real host an SSH host name stands for: its HostName in
// SSH's configuration, or host itself when it isn't an alias or nothing can
// say. It asks `ssh -G`, which reads the configuration as git's own ssh does
// (Include, Match, the system-wide file). In a VM it also reads the host
// user's ~/.ssh/config, which is where a project's remote was made and which
// isn't the VM user's on a Mac (Lima's home is its own); and it reads the
// file itself when ssh can't, being missing or refusing an option of another
// ssh's (a Mac's UseKeychain, on Linux).
func SSHHostName(host string) string {
	if host == "" || strings.HasPrefix(host, "-") {
		return host // never an option to ssh
	}
	if real, ok := sshHosts.Load(host); ok {
		return real.(string)
	}
	real := resolveSSHHost(host, hostSSHConfigs())
	sshHosts.Store(host, real)
	return real
}

// resolveSSHHost is SSHHostName, uncached, with the configuration files to
// read besides the ones ssh reads itself.
func resolveSSHHost(host string, others []string) string {
	real, err := askSSH(host)
	if err != nil {
		if home, herr := os.UserHomeDir(); herr == nil {
			others = append([]string{filepath.Join(home, ".ssh", "config")}, others...)
		}
	} else if !strings.EqualFold(real, host) {
		return real
	}
	for _, file := range others {
		if real, ok := sshConfigHostName(file, host); ok && !strings.EqualFold(real, host) {
			return real
		}
	}
	return host
}

// hostSSHConfigs are the ~/.ssh/config files of the people this machine is a
// VM for, the host user's and in WSL the Windows user's, when they aren't
// this user's own.
func hostSSHConfigs() []string {
	own, _ := os.UserHomeDir()
	var files []string
	for _, home := range []string{hostos.Home(), hostos.WindowsHome()} {
		if home != "" && filepath.Clean(home) != filepath.Clean(own) {
			files = append(files, filepath.Join(home, ".ssh", "config"))
		}
	}
	return files
}

// askSSH is the hostname `ssh -G` says host has.
func askSSH(host string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-G", "--", host).Output()
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(out)) {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), " "); ok && key == "hostname" {
			return strings.TrimSpace(value), nil
		}
	}
	return host, nil
}

// sshConfigHostName is the HostName the SSH configuration file file (a
// ~/.ssh/config) gives host, read the plain way: Host blocks and their
// patterns (*, ? and !), Include, and the first HostName that applies
// winning, as in ssh. A Match block is taken not to apply, since what it
// tests (a command's exit, the canonical host) can't be told here, except
// Match all.
func sshConfigHostName(file, host string) (string, bool) {
	sshDir := filepath.Dir(file)
	r := sshConfigReader{host: strings.ToLower(host), sshDir: sshDir, home: filepath.Dir(sshDir)}
	return r.read(file, 0)
}

// sshConfigReader reads one user's SSH configuration for one host. Relative
// Includes are in that user's ~/.ssh (sshDir) wherever they are written.
type sshConfigReader struct{ host, sshDir, home string }

func (r sshConfigReader) read(file string, depth int) (string, bool) {
	if depth > 16 { // ssh's own limit on nested Includes
		return "", false
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	applies := true
	for line := range strings.Lines(string(data)) {
		key, args := sshConfigLine(line)
		switch strings.ToLower(key) {
		case "host":
			applies = sshHostMatches(r.host, args)
		case "match":
			applies = len(args) == 1 && strings.EqualFold(args[0], "all")
		case "include":
			if !applies {
				continue
			}
			for _, pattern := range args {
				if rest, ok := strings.CutPrefix(pattern, "~/"); ok {
					pattern = filepath.Join(r.home, rest)
				} else if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(r.sshDir, pattern)
				}
				files, _ := filepath.Glob(pattern)
				for _, f := range files {
					if real, ok := r.read(f, depth+1); ok {
						return real, true
					}
				}
			}
		case "hostname":
			if applies && len(args) > 0 {
				return expandSSHTokens(args[0], r.host), true
			}
		}
	}
	return "", false
}

// sshConfigLine splits a line of SSH configuration into its keyword and
// arguments: separated by spaces or one "=", "#" starting a comment, and an
// argument in double quotes keeping its spaces.
func sshConfigLine(line string) (key string, args []string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, nil
	}
	key, rest := line[:i], strings.TrimLeft(line[i:], " \t")
	if r, ok := strings.CutPrefix(rest, "="); ok {
		rest = strings.TrimLeft(r, " \t")
	}
	for rest != "" {
		if rest[0] == '#' {
			break
		}
		var arg string
		if rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return key, args // unterminated: ssh refuses the line
			}
			arg, rest = rest[1:end+1], rest[end+2:]
		} else if j := strings.IndexAny(rest, " \t"); j >= 0 {
			arg, rest = rest[:j], rest[j:]
		} else {
			arg, rest = rest, ""
		}
		args = append(args, arg)
		rest = strings.TrimLeft(rest, " \t")
	}
	return key, args
}

// sshHostMatches is whether a Host line's patterns take host (lowercase):
// one of them matches it, and none of those negated with ! does.
func sshHostMatches(host string, patterns []string) bool {
	matched := false
	for _, p := range patterns {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			if wildMatch(strings.ToLower(neg), host) {
				return false
			}
		} else if wildMatch(strings.ToLower(p), host) {
			matched = true
		}
	}
	return matched
}

// wildMatch matches s against an SSH pattern, where * is any run of
// characters and ? any one.
func wildMatch(pattern, s string) bool {
	for pattern != "" {
		switch pattern[0] {
		case '*':
			for i := len(s); i >= 0; i-- {
				if wildMatch(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
		default:
			if s == "" || s[0] != pattern[0] {
				return false
			}
		}
		pattern, s = pattern[1:], s[1:]
	}
	return s == ""
}

// expandSSHTokens fills in the tokens a HostName may hold: %h, the host as
// given, and %%.
func expandSSHTokens(name, host string) string {
	return strings.NewReplacer("%%", "%", "%h", host).Replace(name)
}
