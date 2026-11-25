package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSSHConfig writes files under a fresh home's .ssh and answers that
// home's ~/.ssh/config.
func writeSSHConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	for name, body := range files {
		p := filepath.Join(home, ".ssh", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(home, ".ssh", "config")
}

// The ~/.ssh/config people really write, read without ssh: Host blocks with
// patterns, Include, Match, and the first HostName that applies winning.
func TestSSHConfigHostName(t *testing.T) {
	t.Parallel()
	config := writeSSHConfig(t, map[string]string{
		"config": `# Two GitHub accounts on two keys
Include config.d/*
Include ~/.ssh/extra

Host github-stefanini
    HostName github.com
    IdentityFile ~/.ssh/id_stefanini
    IdentitiesOnly yes

Host gh-* !gh-skip
	HostName=github.com

Host "quoted-alias"
  HostName "gitlab.com"

Match exec "test -f /never"
  HostName nowhere.example

Host *.internal
  HostName %h.corp.example

Host github-stefanini
  HostName second.example   # the first one wins

Host *
  UseKeychain yes
  AddKeysToAgent yes
`,
		"config.d/work": "Host work-git\n  HostName git.work.example\n",
		"extra":         "Host extra-alias\n  HostName codeberg.org\n",
	})
	for host, want := range map[string]string{
		"github-stefanini": "github.com",
		"GitHub-Stefanini": "github.com",
		"gh-personal":      "github.com",
		"gh-skip":          "",
		"work-git":         "git.work.example",
		"extra-alias":      "codeberg.org",
		"quoted-alias":     "gitlab.com",
		"git.internal":     "git.internal.corp.example",
		"github.com":       "",
		"unknown":          "",
	} {
		got, ok := sshConfigHostName(config, host)
		if want == "" {
			if ok {
				t.Errorf("%s resolved to %q; want no HostName", host, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%s resolved to %q, %v; want %q", host, got, ok, want)
		}
	}
	if _, ok := sshConfigHostName(filepath.Join(t.TempDir(), "none"), "github-stefanini"); ok {
		t.Error("a missing file gave a HostName")
	}
}

// fakeSSH puts an ssh on PATH that answers `ssh -G` with hostname for every
// host, "$h" for the host itself (no alias), or "" to fail as ssh does on an
// option it doesn't know.
func fakeSSH(t *testing.T, hostname string) {
	t.Helper()
	dir := t.TempDir()
	var script string
	switch hostname {
	case "":
		script = "#!/bin/sh\necho 'Bad configuration option: usekeychain' >&2\nexit 255\n"
	case "$h":
		script = "#!/bin/sh\nfor a; do h=$a; done\necho \"hostname $h\"\n"
	default:
		script = "#!/bin/sh\necho \"hostname " + hostname + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// ssh -G is believed when it knows the alias; when it doesn't, or can't
// run, the files it can't read are: the host user's in a VM, and this user's
// own when ssh is missing or refuses its configuration.
func TestResolveSSHHost(t *testing.T) {
	own := writeSSHConfig(t, map[string]string{"config": "Host mine\n  HostName gitlab.com\n"})
	t.Setenv("HOME", filepath.Dir(filepath.Dir(own)))
	mac := writeSSHConfig(t, map[string]string{"config": "Host github-stefanini\n  HostName github.com\n  UseKeychain yes\n"})

	fakeSSH(t, "github.com")
	if got := resolveSSHHost("anything", []string{mac}); got != "github.com" {
		t.Errorf("with ssh knowing it: %q", got)
	}

	fakeSSH(t, "$h") // ssh knows no alias: the Mac's file, in a Lima VM, does
	if got := resolveSSHHost("github-stefanini", []string{mac}); got != "github.com" {
		t.Errorf("with the alias only in the host's file: %q", got)
	}
	if got := resolveSSHHost("gitlab.com", []string{mac}); got != "gitlab.com" {
		t.Errorf("a real host: %q", got)
	}

	fakeSSH(t, "") // refusing UseKeychain, as Linux ssh does
	if got := resolveSSHHost("mine", nil); got != "gitlab.com" {
		t.Errorf("with ssh refusing its configuration: %q; want this user's file read", got)
	}

	t.Setenv("PATH", t.TempDir()) // no ssh at all
	if got := resolveSSHHost("github-stefanini", []string{mac}); got != "github.com" {
		t.Errorf("without ssh: %q", got)
	}
	if got := resolveSSHHost("nobody-knows", []string{mac}); got != "nobody-knows" {
		t.Errorf("an unknown host without ssh: %q", got)
	}
	if got := SSHHostName("-oProxyCommand=boom"); got != "-oProxyCommand=boom" {
		t.Errorf("an option for a host: %q", got)
	}
}
