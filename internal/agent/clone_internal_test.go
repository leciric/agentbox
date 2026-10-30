package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/credentials"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestCloneCopiesARepositoryAndRefusesAFolderThatIsThere(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	gitIn(t, src, "init", "-q", "-b", "main")
	gitIn(t, src, "commit", "-q", "--allow-empty", "-m", "first")
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}}
	dest := filepath.Join(t.TempDir(), "src", "hello")
	var log strings.Builder
	if err := m.Clone(context.Background(), src, dest, "", &log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("no clone in %s: %v", dest, err)
	}
	if !strings.Contains(log.String(), "with this machine's git credentials") {
		t.Errorf("log = %q", log.String())
	}
	if err := m.Clone(context.Background(), src, dest, "", &log); !errors.Is(err, ErrCloneDestExists) {
		t.Errorf("cloning onto %s again = %v, want ErrCloneDestExists", dest, err)
	}
}

func TestCloneThatFailsLeavesNothing(t *testing.T) {
	t.Parallel()
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}}
	dest := filepath.Join(t.TempDir(), "gone")
	err := m.Clone(context.Background(), filepath.Join(t.TempDir(), "nothing"), dest, "", &strings.Builder{})
	if err == nil {
		t.Fatal("cloning nothing worked")
	}
	if _, statErr := os.Lstat(dest); statErr == nil {
		t.Errorf("%s is left behind", dest)
	}
}

// A github.com link is tried with the GitHub account, over https even as a
// git@ link, and again with the machine's own credentials when that fails.
func TestCloneTriesTheGitHubAccountThenTheMachines(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	fake := `#!/bin/sh
echo "token=${GH_TOKEN:+yes} $*" >>` + calls + `
if [ -n "$GH_TOKEN" ]; then echo "remote: Repository not found." >&2; echo "fatal: repository 'https://github.com/acme/secret/' not found" >&2; exit 128; fi
for a; do last=$a; done
mkdir -p "$last/.git"
printf 'Receiving objects:   1%% (1/100)\rReceiving objects:   5%% (5/100)\rReceiving objects:  50%% (50/100)\rReceiving objects: 100%% (100/100), done.\n' >&2
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "")
	m := &Manager{Creds: credentials.Store{Dir: t.TempDir()}}
	if err := m.Creds.SaveGitHubToken("work", "ghp_test"); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	dest := filepath.Join(t.TempDir(), "secret")
	if err := m.Clone(context.Background(), "git@github.com:acme/secret.git", dest, "", &log); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "token=yes") || !strings.Contains(lines[0], "insteadOf=git@github.com:") ||
		!strings.HasPrefix(lines[1], "token= ") || strings.Contains(lines[1], "insteadOf") {
		t.Errorf("git ran as:\n%s", got)
	}
	for _, want := range []string{"with the GitHub account work", "couldn't clone it (repository 'https://github.com/acme/secret/' not found)", "Receiving objects:  50%", "100% (100/100), done."} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
	if strings.Contains(log.String(), "5% (5/100)") {
		t.Errorf("log keeps every redraw:\n%s", log.String())
	}
}

func TestOnGitHub(t *testing.T) {
	t.Parallel()
	for u, want := range map[string]bool{
		"https://github.com/acme/x": true, "https://me@github.com/acme/x.git": true, "git@github.com:acme/x.git": true,
		"ssh://git@github.com/acme/x": true, "https://GitHub.com/acme/x": true,
		"https://gitlab.com/acme/x": false, "git@gitlab.com:acme/x": false, "https://example.com/github.com/x": false, "/home/x": false,
	} {
		if got := onGitHub(u); got != want {
			t.Errorf("onGitHub(%q) = %v", u, got)
		}
	}
}

func TestCloneFailureSaysWhatWentWrong(t *testing.T) {
	t.Parallel()
	fail := errors.New("exit status 128")
	for _, c := range []struct{ out, account, want string }{
		{"remote: Repository not found.\nfatal: repository 'https://github.com/acme/x/' not found", "work", "the GitHub account work and this machine's git credentials have no access"},
		{"git@gitlab.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", "", "this machine's git credentials have no access"},
		{"fatal: unable to access 'https://nowhere.invalid/x/': Could not resolve host: nowhere.invalid", "", "can't reach the host"},
		{"fatal: something else", "", "cloning u: something else"},
	} {
		if err := cloneFailure("u", c.account, c.out, fail); !strings.Contains(err.Error(), c.want) {
			t.Errorf("for %q: %v, want %q", c.out, err, c.want)
		}
	}
}
