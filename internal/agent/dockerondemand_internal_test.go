package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dockerIdle runs docker-idle.sh against stand-ins for systemctl, ss and
// docker: Docker active or not, the socket's clients and the running
// containers as given. It returns what systemctl was asked to stop.
func dockerIdle(t *testing.T, since string, active bool, clients, running string) string {
	t.Helper()
	bin := t.TempDir()
	stopped := filepath.Join(bin, "stopped")
	activeExit := "1"
	if active {
		activeExit = "0"
	}
	for name, body := range map[string]string{
		"systemctl": `case "$1" in is-active) exit ` + activeExit + ` ;; stop) shift; echo "$@" >>` + stopped + ` ;; esac`,
		"ss":        `printf '%s' "$SS_OUT"`,
		"docker":    `printf '%s' "$PS_OUT"`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", dockerIdleScript)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "AGENTBOX_DOCKER_IDLE=300",
		"AGENTBOX_DOCKER_IDLE_SINCE="+since, "SS_OUT="+clients, "PS_OUT="+running)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker-idle.sh: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(stopped)
	return strings.TrimSpace(string(got))
}

func TestDockerIdleStopsDockerOnceIdleLongEnough(t *testing.T) {
	t.Parallel()
	since := filepath.Join(t.TempDir(), "since")
	// First seen idle: noted, not stopped.
	if got := dockerIdle(t, since, true, "", ""); got != "" {
		t.Fatalf("stopped %q the first time it was idle", got)
	}
	if _, err := os.Stat(since); err != nil {
		t.Fatalf("idle wasn't noted: %v", err)
	}
	// Still within the five minutes.
	if got := dockerIdle(t, since, true, "", ""); got != "" {
		t.Fatalf("stopped %q within the idle time", got)
	}
	// Idle long enough.
	old := time.Now().Add(-6 * time.Minute)
	if err := os.Chtimes(since, old, old); err != nil {
		t.Fatal(err)
	}
	if got := dockerIdle(t, since, true, "", ""); got != "docker.service containerd.service" {
		t.Fatalf("stopped %q, want docker.service containerd.service", got)
	}
}

func TestDockerIdleLeavesADockerInUse(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		active           bool
		clients, running string
	}{
		"a client on its socket": {true, "u_str ESTAB 0 0 /run/docker.sock 1234 * 5678\n", ""},
		"a container running":    {true, "", "0123456789ab\n"},
		"stopped already":        {false, "", ""},
	} {
		since := filepath.Join(t.TempDir(), "since")
		old := time.Now().Add(-time.Hour)
		if err := os.WriteFile(since, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(since, old, old); err != nil {
			t.Fatal(err)
		}
		if got := dockerIdle(t, since, c.active, c.clients, c.running); got != "" {
			t.Errorf("%s: stopped %q", name, got)
		}
		if _, err := os.Stat(since); err == nil {
			t.Errorf("%s: still noted as idle", name)
		}
	}
}

// The setup carries the idle script whole, and is a shell script sh can read.
func TestDockerOnDemandScript(t *testing.T) {
	t.Parallel()
	script := strings.Replace(dockerOnDemand, "@SCRIPT@", dockerIdleScript, 1)
	if !strings.Contains(script, "\n"+dockerIdleScript+"AGENTBOX_EOF\n") {
		t.Error("the idle script isn't in the setup whole")
	}
	if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Errorf("bash -n: %v\n%s", err, out)
	}
	for _, want := range []string{"systemctl disable -q docker.service containerd.service", "enable -q --now docker.socket agentbox-docker-idle.timer"} {
		if !strings.Contains(script, want) {
			t.Errorf("the setup doesn't %s", want)
		}
	}
}
