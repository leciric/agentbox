package gitcred_test

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/gitcred"
)

// file writes one of the machine's config files and returns its path.
func file(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cfg builds a Config whose two files are the test's own, so the machine
// running the tests can't change an answer: the ssh config may be a path that
// doesn't exist, which is a machine with none.
func cfg(t *testing.T, gitconfig, sshconfig string) gitcred.Config {
	t.Helper()
	c := gitcred.Config{
		GitConfig: file(t, "gitconfig", ""),
		SSHConfig: filepath.Join(t.TempDir(), "ssh-config"),
	}
	if gitconfig != "" {
		c.GitConfig = gitconfig
	}
	if sshconfig != "" {
		c.SSHConfig = sshconfig
	}
	return c
}

func resolve(t *testing.T, raw string, c gitcred.Config) (string, error) {
	t.Helper()
	return gitcred.Resolve(context.Background(), raw, c)
}

func TestForgeAlias(t *testing.T) {
	gitconfig := file(t, "gitconfig", `[url "https://hub.example.ts.net/"]
	insteadOf = forge:
`)
	got, err := resolve(t, "forge:h4ad/agentbox.git", cfg(t, gitconfig, ""))
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://hub.example.ts.net/h4ad/agentbox.git"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// git applies the longest matching insteadOf prefix, so a rule for one owner
// wins over the rule for the whole alias.
func TestInsteadOfLongestMatch(t *testing.T) {
	gitconfig := file(t, "gitconfig", `[url "https://hub.example.ts.net/"]
	insteadOf = forge:
[url "https://git.h4ad.example/"]
	insteadOf = forge:h4ad/
`)
	c := cfg(t, gitconfig, "")
	got, err := resolve(t, "forge:h4ad/agentbox.git", c)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://git.h4ad.example/agentbox.git"; got != want {
		t.Errorf("an owner's rule: Resolve = %q, want %q", got, want)
	}
	got, err = resolve(t, "forge:other/agentbox.git", c)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://hub.example.ts.net/other/agentbox.git"; got != want {
		t.Errorf("the alias's own rule: Resolve = %q, want %q", got, want)
	}
}

// A rewrite applies to any URL, not just to an alias: here an https remote
// becomes the ssh one the user's git would really use.
func TestInsteadOfOnAnHTTPSURL(t *testing.T) {
	gitconfig := file(t, "gitconfig", `[url "git@github.com:"]
	insteadOf = https://github.com/
`)
	got, err := resolve(t, "https://github.com/h4ad/agentbox.git", cfg(t, gitconfig, ""))
	if err != nil {
		t.Fatal(err)
	}
	if want := "git@github.com:h4ad/agentbox.git"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestSSHShortName(t *testing.T) {
	sshconfig := file(t, "ssh-config", `# the private forge
Host hub
	HostName hub.example.ts.net
	User git
	Port 2222

Host *
	User nobody
`)
	c := cfg(t, "", sshconfig)
	for _, raw := range []string{
		"git@hub:h4ad/agentbox.git",
		"ssh://hub/h4ad/agentbox.git",
		"ssh://git@hub/h4ad/agentbox.git",
	} {
		got, err := resolve(t, raw, c)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if want := "ssh://git@hub.example.ts.net:2222/h4ad/agentbox.git"; got != want {
			t.Errorf("%s resolves to %q, want %q", raw, got, want)
		}
	}
}

// The alias is resolved first and the ssh name second: a rule that points at a
// short name is still handed to the pod as the real host.
func TestAliasThenSSHName(t *testing.T) {
	gitconfig := file(t, "gitconfig", `[url "ssh://git@hub/"]
	insteadOf = forge:
`)
	sshconfig := file(t, "ssh-config", `Host hub
	HostName hub.example.ts.net
	Port 2222
`)
	got, err := resolve(t, "forge:h4ad/agentbox.git", cfg(t, gitconfig, sshconfig))
	if err != nil {
		t.Fatal(err)
	}
	if want := "ssh://git@hub.example.ts.net:2222/h4ad/agentbox.git"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// A name no Host block mentions may resolve through DNS, so it is left as it
// is rather than guessed at.
func TestUnknownShortNameIsLeftAlone(t *testing.T) {
	c := cfg(t, "", "")
	for _, raw := range []string{
		"git@example.com:h4ad/agentbox.git",
		"ssh://example.com/h4ad/agentbox.git",
		"https://example.com/h4ad/agentbox.git",
	} {
		if got, err := resolve(t, raw, c); err != nil || got != raw {
			t.Errorf("%s resolves to %q, %v, want it unchanged", raw, got, err)
		}
	}
}

func TestForgeAliasWithoutARule(t *testing.T) {
	_, err := resolve(t, "forge:h4ad/agentbox.git", cfg(t, "", ""))
	if err == nil {
		t.Fatal("an alias nothing resolves was accepted")
	}
	for _, want := range []string{"forge:h4ad/agentbox.git", "url.<https base>.insteadof forge:", "git config --global"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error doesn't say %q: %v", want, err)
		}
	}
}

func TestPasswordInAURLIsRefused(t *testing.T) {
	c := cfg(t, "", "")
	_, err := resolve(t, "https://git:s3cr3t@hub.example.com/h4ad/agentbox.git", c)
	if err == nil {
		t.Fatal("a URL with a password was resolved")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("the error repeats the password: %v", err)
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Errorf("the error doesn't say what to do instead: %v", err)
	}

	// And one a rewrite brings in is refused too.
	gitconfig := file(t, "gitconfig", `[url "https://git:s3cr3t@hub.example.com/"]
	insteadOf = forge:
`)
	_, err = resolve(t, "forge:h4ad/agentbox.git", cfg(t, gitconfig, ""))
	if err == nil {
		t.Fatal("a rewrite that adds a password was accepted")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("the error repeats the password: %v", err)
	}
}

func TestRedact(t *testing.T) {
	token := "ghp_a/b+c=d"
	escaped := url.QueryEscape(token)
	text := "fatal: could not read from https://git:" + token + "@hub/x and " + escaped + " again"
	got := gitcred.Redact(text, "", token, token)
	if strings.Contains(got, token) || strings.Contains(got, escaped) {
		t.Errorf("Redact left a secret in %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("Redact = %q, want the secret replaced", got)
	}

	// The longer secret is removed whole rather than in pieces, and a text
	// with no secret in it is unchanged.
	if got := gitcred.Redact("abcdef", "abc", "abcdef"); got != "***" {
		t.Errorf("Redact of overlapping secrets = %q, want ***", got)
	}
	if got := gitcred.Redact("nothing to hide", "secret"); got != "nothing to hide" {
		t.Errorf("Redact changed a text with no secret: %q", got)
	}
}

func TestCredentialHelper(t *testing.T) {
	const (
		host    = "hub.example.ts.net"
		user    = "git"
		token   = "s3cr3t-token"
		wantGet = "username=git\npassword=s3cr3t-token\n"
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "git-credential")
	script := gitcred.CredentialHelper(host, user, token)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, host) {
		t.Errorf("the helper doesn't say which host it is for:\n%s", script)
	}
	// The token is a variable in the file and nothing else: any command it
	// appeared as an argument of would put it in the pod's process list.
	for i, line := range strings.Split(script, "\n") {
		if strings.Contains(line, token) && !strings.HasPrefix(line, "password=") {
			t.Errorf("line %d uses the token somewhere other than the password assignment: %q", i+1, line)
		}
	}

	// git runs the file through sh, by name: sh <path> get.
	for _, tc := range []struct{ op, want string }{
		{"get", wantGet},
		{"store", ""},
		{"erase", ""},
	} {
		out, err := exec.Command("sh", path, tc.op).Output()
		if err != nil {
			t.Fatalf("%s: %v", tc.op, err)
		}
		if string(out) != tc.want {
			t.Errorf("%s printed %q, want %q", tc.op, out, tc.want)
		}
	}

	// A token with a quote in it is still one word, and still answered whole.
	quoted := gitcred.CredentialHelper(host, user, "it's-a-token")
	quotedPath := filepath.Join(dir, "quoted")
	if err := os.WriteFile(quotedPath, []byte(quoted), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", quotedPath, "get").Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := "username=git\npassword=it's-a-token\n"; string(out) != want {
		t.Errorf("get with a quote in the token printed %q, want %q", out, want)
	}
}

func TestSSHCommand(t *testing.T) {
	got := gitcred.SSHCommand("/run/agentbox/id key")
	if !strings.Contains(got, `-i '/run/agentbox/id key'`) {
		t.Errorf("SSHCommand = %q, want the key quoted", got)
	}
	for _, want := range []string{"IdentitiesOnly=yes", "BatchMode=yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("SSHCommand = %q, want %s", got, want)
		}
	}
}
