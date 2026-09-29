package chv

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/paths"
)

func testPaths(t *testing.T) paths.Paths {
	dir := t.TempDir()
	return paths.Paths{Config: filepath.Join(dir, "config"), Data: filepath.Join(dir, "data")}
}

func testLayout(t *testing.T) Layout { return NewLayout(testPaths(t), "agentbox") }

func TestSSHArgs(t *testing.T) {
	l := testLayout(t)
	c := Config{Name: "agentbox", User: "lint"}
	args := SSHArgs(c, l, "/opt/Agent Box/agentbox", "/home/lint/src", true, []string{"agentbox", "ls"})
	if args[0] != "ssh" {
		t.Fatalf("args = %q", args)
	}
	for _, want := range []string{
		"ProxyCommand='/opt/Agent Box/agentbox' vm proxy 22",
		"UserKnownHostsFile=" + l.KnownHosts(),
		"StrictHostKeyChecking=accept-new",
		"BatchMode=yes",
		"ControlPath=" + l.ControlSocket(),
	} {
		if !slices.Contains(args, want) {
			t.Errorf("no -o %s in %q", want, args)
		}
	}
	n := len(args)
	if args[n-3] != "-t" || args[n-2] != sshHost || args[n-1] != "cd /home/lint/src && exec agentbox ls" {
		t.Errorf("ends with %q", args[n-3:])
	}
	if i := slices.Index(args, "-l"); i < 0 || args[i+1] != "lint" {
		t.Errorf("no -l lint in %q", args)
	}
	args = SSHArgs(c, l, "/usr/bin/agentbox", "", false, nil)
	if args[len(args)-2] != "-T" || args[len(args)-1] != sshHost {
		t.Errorf("no command: ends with %q", args[len(args)-2:])
	}
}

func TestSSHArgsEscapesTokens(t *testing.T) {
	l := NewLayout(paths.Paths{Data: "/home/a%b c"}, "agentbox")
	args := SSHArgs(Config{User: "u"}, l, "/home/a%b c/agentbox", "", false, nil)
	for _, want := range []string{
		`ControlPath="/home/a%%b c/vm/agentbox/run/ssh.ctl"`,
		`ProxyCommand='/home/a%%b c/agentbox' vm proxy 22`,
	} {
		if !slices.Contains(args, want) {
			t.Errorf("no -o %s in %q", want, args)
		}
	}
}

// TestRemoteCommandQuoting runs remoteCommand's string through a shell, as
// sshd does, and checks the program gets its arguments back as they were.
func TestRemoteCommandQuoting(t *testing.T) {
	dir := t.TempDir()
	wd := filepath.Join(dir, "it's here")
	if err := exec.Command("mkdir", wd).Run(); err != nil {
		t.Fatal(err)
	}
	args := []string{"plain", "two words", "it's", `$HOME`, "`id`", `a\b`, "-n", "", "*", "x;y", "~", "%s"}
	argv := append([]string{"sh", "-c", `printf '%s|' "$@"; pwd`, "sh"}, args...)
	cmd := remoteCommand(wd, argv)
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	want := strings.Join(args, "|") + "|" + wd + "\n"
	if string(out) != want {
		t.Errorf("sh -c %s\n printed %q\n want    %q", cmd, out, want)
	}
	if strings.HasPrefix(remoteCommand("", []string{"-x"}), "-") {
		t.Error("a command ssh would take for an option")
	}
	if got := remoteCommand("/w", nil); got != `cd /w && exec "$SHELL" -l` {
		t.Errorf("remoteCommand(no argv) = %q", got)
	}
}
