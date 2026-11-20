package sshconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func files(t *testing.T) Files {
	t.Helper()
	dir := t.TempDir()
	return Files{Dir: filepath.Join(dir, "data", "ssh"), UserConfig: filepath.Join(dir, "home", ".ssh", "config")}
}

func TestInstanceIsTheHostNameLessItsSuffix(t *testing.T) {
	for host, want := range map[string]string{
		"ab-pawly-agent-01.agentbox":                  "ab-pawly-agent-01",
		"ab-my-app-fix-login.agentbox":                "ab-my-app-fix-login",
		"ab-pawly-agent-01":                           "", // not ours
		"pawly.agentbox":                              "", // not an instance
		"ab-Pawly-agent.agentbox":                     "", // instances are lowercase
		"ab-x;rm -rf.agentbox":                        "",
		"ab-" + strings.Repeat("a", 61) + ".agentbox": "", // longer than Incus allows
	} {
		if got := Instance(host); got != want {
			t.Errorf("Instance(%q) = %q, want %q", host, got, want)
		}
	}
	if Instance(Host("ab-pawly-agent-01")) != "ab-pawly-agent-01" {
		t.Error("Host and Instance don't round-trip")
	}
}

func TestEnsureMakesTheKeyConfigAndIncludeOnce(t *testing.T) {
	f := files(t)
	if err := os.MkdirAll(filepath.Dir(f.UserConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	mine := "Host github.com\n\tUser git\n"
	if err := os.WriteFile(f.UserConfig, []byte(mine), 0o640); err != nil {
		t.Fatal(err)
	}
	setup, err := Ensure(f, "/opt/agent box/agentbox", "ana")
	if err != nil {
		t.Fatal(err)
	}
	if setup.User != "ana" || setup.HostSuffix != ".agentbox" || setup.Config != filepath.Join(f.Dir, "config") {
		t.Errorf("setup = %+v", setup)
	}
	key, _ := os.ReadFile(f.key())
	if fi, err := os.Stat(f.key()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the private key isn't the user's alone: %v %v", fi.Mode(), err)
	}
	pub, _ := os.ReadFile(f.key() + ".pub")
	if !ValidKey(strings.TrimSpace(string(pub))) {
		t.Errorf("the public key %q isn't one", pub)
	}
	config, _ := os.ReadFile(f.config())
	for _, want := range []string{
		"Host *.agentbox\n",
		"\tUser ana\n",
		`IdentityFile "` + f.key() + `"`,
		`ProxyCommand '/opt/agent box/agentbox' ssh-proxy --key '` + strings.TrimSpace(string(pub)) + `' %n`,
		"StrictHostKeyChecking no",
	} {
		if !strings.Contains(string(config), want) {
			t.Errorf("config lacks %q:\n%s", want, config)
		}
	}
	user, _ := os.ReadFile(f.UserConfig)
	want := includeMarker + "\n" + `Include "` + f.config() + `"` + "\n\n" + mine
	if string(user) != want {
		t.Errorf("~/.ssh/config =\n%s\nwant\n%s", user, want)
	}
	if fi, _ := os.Stat(f.UserConfig); fi.Mode().Perm() != 0o640 {
		t.Errorf("~/.ssh/config's mode changed to %v", fi.Mode().Perm())
	}

	// Again, as the apps do before each open: the same key, one Include.
	if _, err := Ensure(f, "/opt/agent box/agentbox", "ana"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(f.key()); string(again) != string(key) {
		t.Error("the key was made again")
	}
	if again, _ := os.ReadFile(f.UserConfig); string(again) != want {
		t.Errorf("a second Ensure changed ~/.ssh/config:\n%s", again)
	}

	// Moved agentbox: the ProxyCommand follows.
	if _, err := Ensure(f, "/usr/local/bin/agentbox", "ana"); err != nil {
		t.Fatal(err)
	}
	if config, _ := os.ReadFile(f.config()); !strings.Contains(string(config), "ProxyCommand '/usr/local/bin/agentbox'") {
		t.Errorf("the ProxyCommand didn't follow agentbox:\n%s", config)
	}

	if err := Remove(f); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(f.UserConfig); string(after) != mine {
		t.Errorf("--remove left ~/.ssh/config as\n%q\nwant\n%q", after, mine)
	}
	if _, err := os.Stat(f.Dir); !os.IsNotExist(err) {
		t.Errorf("--remove left %s: %v", f.Dir, err)
	}
}

func TestEnsureMakesSSHConfigWhenThereIsNone(t *testing.T) {
	f := files(t)
	if _, err := Ensure(f, "/usr/bin/agentbox", "ana"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.UserConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != includeMarker+"\n"+`Include "`+f.config()+`"`+"\n" {
		t.Errorf("~/.ssh/config = %q", b)
	}
	if fi, _ := os.Stat(filepath.Dir(f.UserConfig)); fi.Mode().Perm() != 0o700 {
		t.Errorf("~/.ssh is %v", fi.Mode().Perm())
	}
	if err := Remove(f); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f.UserConfig); len(b) != 0 {
		t.Errorf("--remove left %q", b)
	}
}

func TestRenderEscapesPercentForSSH(t *testing.T) {
	got := Render("/home/a%b/agentbox", "/home/a%b/key", "ssh-ed25519 AAAA agentbox", "ana")
	if !strings.Contains(got, `IdentityFile "/home/a%%b/key"`) || !strings.Contains(got, `ProxyCommand '/home/a%%b/agentbox'`) {
		t.Errorf("%% isn't doubled:\n%s", got)
	}
}

func TestValidKeyIsOneKeyWithNoOptions(t *testing.T) {
	f := files(t)
	pub, err := ensureKey(f.key())
	if err != nil {
		t.Fatal(err)
	}
	if !ValidKey(pub) {
		t.Errorf("ValidKey(%q) = false", pub)
	}
	for _, bad := range []string{"", "not a key", `command="sh" ` + pub, pub + "\n" + pub, pub + "\nssh-rsa AAAA"} {
		if ValidKey(bad) {
			t.Errorf("ValidKey(%q) = true", bad)
		}
	}
}

func TestServerScriptIsShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	if out, err := exec.Command(sh, "-n", "-c", ServerScript).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}
