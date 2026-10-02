package agent_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
)

// An agent started again boots with its in-agent API device already there,
// whose socket systemd's tmpfs on /run then hides: EnsureAgentAPI plugs it in
// again. One whose socket is there, or that can't be asked, is left alone.
func TestEnsureAgentAPIPlugsAHiddenSocketInAgain(t *testing.T) {
	for _, tc := range []struct {
		name, socket string
		replug       bool
	}{
		{"hidden", "missing", true},
		{"there", "there", false},
		{"stopped", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execAnswer := `echo "` + tc.socket + `"`
			if tc.socket == "" {
				execAnswer = `echo "Error: Instance is not running" >&2; exit 1`
			}
			inc, calls := loggingIncus(t, `case "$1" in
  query) echo '{"devices": {"agentbox": {"type": "proxy", "listen": "unix:/run/agentbox.sock"}}}' ;;
  exec) `+execAnswer+` ;;
esac`)
			f := setup(t, inc)
			f.m.AgentSocket = func(instance string) string { return "/t/sockets/" + instance + ".sock" }
			if err := f.m.EnsureAgentAPI(context.Background(), state.Agent{Instance: "ab-agent-01"}); err != nil {
				t.Fatal(err)
			}
			var removed, added bool
			for _, c := range calls() {
				removed = removed || strings.HasPrefix(c, "config device remove ab-agent-01 agentbox")
				added = added || strings.HasPrefix(c, "config device add ab-agent-01 agentbox proxy")
			}
			if removed != tc.replug || added != tc.replug {
				t.Errorf("removed %t, added %t, want both %t: %q", removed, added, tc.replug, calls())
			}
		})
	}
}

// An agent Incus started itself is asked once it has booted, when systemd has
// mounted its /run, and has its device plugged in again then.
func TestRestoreAgentAPISocketWaitsForTheBoot(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in exec) echo missing ;; esac`)
	f := setup(t, inc)
	f.m.AgentSocket = func(instance string) string { return "/t/sockets/" + instance + ".sock" }
	if err := f.m.RestoreAgentAPISocket(context.Background(), state.Agent{Instance: "ab-agent-01"}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls(), "\n")
	if !strings.Contains(got, "systemctl is-system-running --wait") {
		t.Errorf("the socket was looked for without waiting for the boot:\n%s", got)
	}
	if !strings.Contains(got, "config device remove ab-agent-01 agentbox") || !strings.Contains(got, "config device add ab-agent-01 agentbox proxy") {
		t.Errorf("the device wasn't plugged in again:\n%s", got)
	}
}

// A running agent that can't be asked once it has booted is an error, so that
// the daemon asks again once Incus answers, rather than leaving its socket
// hidden.
func TestRestoreAgentAPISocketFailsWhenTheAgentCantBeAsked(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in exec) echo "Error: EOF" >&2; exit 1 ;; esac`)
	f := setup(t, inc)
	f.m.AgentSocket = func(instance string) string { return "/t/sockets/" + instance + ".sock" }
	if err := f.m.RestoreAgentAPISocket(context.Background(), state.Agent{Instance: "ab-agent-01"}); err == nil {
		t.Fatal("no error from an agent that couldn't be asked")
	}
	if got := strings.Join(calls(), "\n"); strings.Contains(got, "config device") {
		t.Errorf("the device was touched:\n%s", got)
	}
}

// TestStartRechecksAHiddenSocketAfterTheBoot reproduces the race a stopped
// agent's Start hit in production (organic/agent-73, agent-75, 2026-10-01):
// EnsureAgentAPI's immediate check (replugHiddenSocket's boot=false) runs
// right after Incus.Start, before systemd has mounted its /run tmpfs, finds
// the socket there and leaves it alone; the tmpfs then hides it. Start's
// background recheck, which waits for the boot first, must find it missing
// then and plug the device in again.
func TestStartRechecksAHiddenSocketAfterTheBoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOOTED", filepath.Join(dir, "booted"))
	t.Setenv("CHECKED", filepath.Join(dir, "checked"))
	inc, calls := loggingIncus(t, `case "$1" in
  list)
    if [ -f "$BOOTED" ]; then
      echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]'
    else
      echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]'
    fi ;;
  start) touch "$BOOTED" ;;
  query) echo '{"config": {}, "devices": {"agentbox": {"type":"proxy","listen":"unix:/run/agentbox.sock"}}}' ;;
  exec)
    if [ "$4" = "sh" ] && [ "$5" = "-c" ]; then
      case "$6" in
        *agentbox.sock*)
          if [ -f "$CHECKED" ]; then echo missing; else touch "$CHECKED"; echo there; fi ;;
      esac
    fi ;;
esac
exit 0`)
	f := setup(t, inc)
	f.m.AgentSocket = func(instance string) string { return "/t/sockets/" + instance + ".sock" }
	recheck := make(chan struct{})
	f.m.RecheckAgentAPI = func(run func()) {
		run()
		close(recheck)
	}
	a := destroyFixture(t, f)

	if _, err := f.m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	select {
	case <-recheck:
	case <-time.After(5 * time.Second):
		t.Fatal("Start never ran its background recheck")
	}

	var removed, added bool
	for _, c := range calls() {
		removed = removed || strings.HasPrefix(c, "config device remove ab-hello-stack-agent-01 agentbox")
		added = added || strings.HasPrefix(c, "config device add ab-hello-stack-agent-01 agentbox proxy")
	}
	if !removed || !added {
		t.Errorf("a socket hidden only after the boot should still be plugged in again: removed %t, added %t\n%s",
			removed, added, strings.Join(calls(), "\n"))
	}
}
