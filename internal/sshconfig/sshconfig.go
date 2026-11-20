// Package sshconfig lets ssh, and the editors that connect over it (VS Code's
// Remote-SSH, Cursor, Windsurf, VSCodium, Zed), reach an agent's machine as
// <instance>.agentbox, with nothing for the user to set up: the apps' "Open in
// <editor>" opens the agent's worktree inside its machine that way.
//
// On the user's computer, `agentbox ssh-config` keeps three files in AgentBox's
// data folder's ssh/: a key of this installation's (id_ed25519), and a config
// with one Host *.agentbox block whose ProxyCommand is `agentbox ssh-proxy`.
// The user's ~/.ssh/config gets one Include line for it, at its top, and
// `agentbox ssh-config --remove` takes that line and the folder out again.
//
// `agentbox ssh-proxy` runs where the daemon is (the VM's agentbox, which the
// front end forwards it to with ssh's stdin and stdout, or this machine's in
// host mode): it wakes the agent if it's stopped or paused, and runs
// ServerScript in its machine through incus exec, which authorizes the key
// and becomes `sshd -i` for that one connection. No sshd service listens in
// agents' machines, and nothing is reachable from the network: the only way
// in is through agentbox, which already holds the daemon's socket.
package sshconfig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"agentbox/internal/paths"
)

// Suffix makes an agent's Incus instance (ab-<project>-<agent>, unique and a
// valid host name) the host name ssh knows its machine by.
const Suffix = ".agentbox"

// Host is the host name of the machine of the agent whose instance this is.
func Host(instance string) string { return instance + Suffix }

var instanceName = regexp.MustCompile(`^ab-[a-z0-9]+(-[a-z0-9]+)*$`)

// Instance is the agent's instance a host name names, "" when it names none.
func Instance(host string) string {
	inst, ok := strings.CutSuffix(host, Suffix)
	if !ok || len(inst) > 63 || !instanceName.MatchString(inst) {
		return ""
	}
	return inst
}

// Setup is what `agentbox ssh-config --json` tells the apps: the user ssh logs
// in as, and how an instance's name becomes its host name.
type Setup struct {
	User       string `json:"user"`
	HostSuffix string `json:"hostSuffix"`
	Config     string `json:"config"`     // AgentBox's ssh config, which ~/.ssh/config includes
	UserConfig string `json:"userConfig"` // ~/.ssh/config
}

// Files are where Ensure writes: AgentBox's folder for ssh, and the user's own
// ssh config.
type Files struct {
	Dir        string
	UserConfig string
}

// DefaultFiles are this user's: the data folder's ssh/, and ~/.ssh/config.
func DefaultFiles() (Files, error) {
	p, err := paths.Default()
	if err != nil {
		return Files{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Files{}, err
	}
	return Files{Dir: filepath.Join(p.Data, "ssh"), UserConfig: filepath.Join(home, ".ssh", "config")}, nil
}

func (f Files) key() string    { return filepath.Join(f.Dir, "id_ed25519") }
func (f Files) config() string { return filepath.Join(f.Dir, "config") }

// Ensure makes the key if there's none, writes AgentBox's config for bin (this
// agentbox, which ssh runs as the ProxyCommand) and user, and includes it from
// the user's config. Each step leaves what's already right alone.
func Ensure(f Files, bin, user string) (Setup, error) {
	pub, err := ensureKey(f.key())
	if err != nil {
		return Setup{}, err
	}
	if err := writeIfChanged(f.config(), []byte(Render(bin, f.key(), pub, user)), 0o600); err != nil {
		return Setup{}, err
	}
	if err := ensureInclude(f.UserConfig, f.config()); err != nil {
		return Setup{}, err
	}
	return Setup{User: user, HostSuffix: Suffix, Config: f.config(), UserConfig: f.UserConfig}, nil
}

// Remove undoes Ensure: the Include line, and AgentBox's folder for ssh.
func Remove(f Files) error {
	if err := removeInclude(f.UserConfig, f.config()); err != nil {
		return err
	}
	return os.RemoveAll(f.Dir)
}

// CurrentUser is the user ssh logs in as: the one running this, whose name
// the VM's user, and so each agent's machine's, has on a Mac and on Linux.
func CurrentUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// ensureKey makes an ed25519 key at path, with its .pub beside it, unless
// there is one, and returns the public half as an authorized_keys line.
func ensureKey(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			return "", fmt.Errorf("AgentBox's ssh key %s: %w (remove it, and agentbox ssh-config makes another)", path, err)
		}
		return authorizedLine(signer.PublicKey()), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "agentbox")
	if err != nil {
		return "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	line := authorizedLine(sshPub)
	if err := os.WriteFile(path+".pub", []byte(line+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	return line, nil
}

func authorizedLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " agentbox"
}

// ValidKey reports whether key is one public key as an authorized_keys line
// has it, with no options: what ssh-proxy writes into a machine.
func ValidKey(key string) bool {
	if strings.ContainsAny(key, "\r\n") {
		return false
	}
	k, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(key))
	return err == nil && k != nil && len(options) == 0 && len(bytes.TrimSpace(rest)) == 0
}

// Render is AgentBox's ssh config: every <instance>.agentbox reached through
// `agentbox ssh-proxy`, with this installation's key. The machine's host key
// isn't checked: there is no network between ssh and the machine, only
// agentbox and incus exec, and a machine remade under the same name (or one
// whose image changed) would otherwise stop ssh with a warning.
func Render(bin, key, pub, user string) string {
	var b strings.Builder
	b.WriteString("# Written by AgentBox (agentbox ssh-config), and rewritten as it changes: how\n")
	b.WriteString("# ssh and editors reach an agent's machine, as <instance>.agentbox, through\n")
	b.WriteString("# agentbox itself. agentbox ssh-config --remove takes it out of ~/.ssh/config.\n")
	b.WriteString("Host *" + Suffix + "\n")
	fmt.Fprintf(&b, "\tUser %s\n", user)
	fmt.Fprintf(&b, "\tIdentityFile %s\n", configQuote(key))
	b.WriteString("\tIdentitiesOnly yes\n")
	fmt.Fprintf(&b, "\tProxyCommand %s ssh-proxy --key %s %%n\n", proxyArg(bin), proxyArg(pub))
	b.WriteString("\tStrictHostKeyChecking no\n")
	b.WriteString("\tUserKnownHostsFile /dev/null\n")
	b.WriteString("\tLogLevel ERROR\n")
	return b.String()
}

// configQuote is a path as ssh_config takes one: in double quotes (a Mac's
// home may have a space), with % doubled, as ssh expands its % tokens.
func configQuote(path string) string {
	return `"` + strings.ReplaceAll(path, "%", "%%") + `"`
}

// proxyArg is one word of the ProxyCommand, which ssh hands to the shell after
// expanding its % tokens: single-quoted, with % doubled.
func proxyArg(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "'", `'\''`), "%", "%%") + "'"
}

// includeMarker is the comment above the Include line, which says what it is
// and is how removeInclude knows the lines are AgentBox's.
const includeMarker = "# Added by AgentBox: agents' machines as <instance>.agentbox (agentbox ssh-config --remove takes this out)"

func includeLine(path string) string { return "Include " + configQuote(path) }

// ensureInclude puts the Include line at the top of the user's config, where
// it applies to every host, unless it's there already. The config and ~/.ssh
// are made if missing, private as ssh wants them.
func ensureInclude(userConfig, path string) error {
	b, err := os.ReadFile(userConfig)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == includeLine(path) {
			return nil
		}
	}
	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(userConfig); err == nil {
		mode = fi.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(userConfig), 0o700); err != nil {
		return err
	}
	head := includeMarker + "\n" + includeLine(path) + "\n"
	if len(b) > 0 {
		head += "\n"
	}
	return writeFile(userConfig, append([]byte(head), b...), mode)
}

// removeInclude takes ensureInclude's lines out of the user's config, and the
// blank line it put after them.
func removeInclude(userConfig, path string) error {
	b, err := os.ReadFile(userConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == includeMarker && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == includeLine(path) {
			i++
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				i++
			}
			continue
		}
		if strings.TrimSpace(lines[i]) == includeLine(path) {
			continue
		}
		out = append(out, lines[i])
	}
	after := strings.Join(out, "\n")
	if after == string(b) {
		return nil
	}
	// A config AgentBox made goes with its line.
	if strings.TrimSpace(after) == "" {
		return os.Remove(userConfig)
	}
	fi, err := os.Stat(userConfig)
	if err != nil {
		return err
	}
	return writeFile(userConfig, []byte(after), fi.Mode().Perm())
}

func writeIfChanged(path string, b []byte, mode fs.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFile(path, b, mode)
}

// writeFile replaces path's contents through a file beside it, so ssh never
// reads half of one. A symlinked config (a dotfiles repository's) is written
// where it points.
func writeFile(path string, b []byte, mode fs.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentbox-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
