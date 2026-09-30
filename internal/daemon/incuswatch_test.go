package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// watchedIncus is fakeIncus for the watch on Incus: /1.0 hangs while the file
// $INCUS_HANG exists, as an Incus does whose incusd died while systemd holds
// its socket, and asking an agent anything fails while $INCUS_EXEC_FAIL does.
// The agent's socket is always hidden, so plugging it in again shows in the
// log.
const watchedIncus = `case "$1" in
  query)
    case "$2" in
      /1.0)
        [ -e "$INCUS_HANG" ] && exec sleep 30
        echo '{}' ;;
      *) echo '{"config": {}, "devices": {"agentbox": {"type": "proxy", "listen": "unix:/run/agentbox.sock"}}}' ;;
    esac ;;
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running"}]' ;;
  exec)
    [ -e "$INCUS_EXEC_FAIL" ] && { echo "Error: EOF" >&2; exit 1; }
    echo missing ;;
  config) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0
`

func startWatchedIncus(t *testing.T) (testDaemon, string) {
	t.Helper()
	root := t.TempDir()
	hang := filepath.Join(root, "hang")
	d := startTestDaemon(t, root, watchedIncus, testConfig{env: map[string]string{
		"INCUS_HANG": hang, "INCUS_EXEC_FAIL": filepath.Join(root, "exec-fail"),
	}})
	w := d.srv.incus
	w.probeTimeout = 300 * time.Millisecond
	w.usable = func() bool { return true }
	return d, hang
}

func plugged(t *testing.T, d testDaemon) bool {
	t.Helper()
	got, _ := os.ReadFile(filepath.Join(d.root, "incus.log"))
	return strings.Contains(string(got), "config device remove ab-hello-stack-agent-01 agentbox") &&
		strings.Contains(string(got), "config device add ab-hello-stack-agent-01 agentbox proxy")
}

// An Incus that accepts connections and answers none makes every call fail at
// once, and the API with them, rather than wait; in the VM the daemon restarts
// incus.service once incusd is gone; and when Incus answers again, the agents'
// sockets, which a boot with Incus down left hidden, are plugged in again.
func TestIncusWatchFailsFastRestartsAndPlugsSocketsAgain(t *testing.T) {
	t.Parallel()
	d, hang := startWatchedIncus(t)
	ctx := context.Background()
	addTestAgent(t, d)
	mustWrite(t, hang, "")

	w := d.srv.incus
	w.deadGrace, w.hungGrace, w.restartEvery = 0, time.Hour, 0
	w.unit = func(context.Context) (incusUnit, error) {
		return incusUnit{State: "activating/start-post", MainPID: 0}, nil
	}
	restarts := 0
	w.restart = func(context.Context) error {
		restarts++
		return os.Remove(hang) // systemd starts a new incusd, which answers
	}

	if d.srv.checkIncus(ctx) {
		t.Fatal("an Incus that doesn't answer had the sockets plugged in")
	}
	if restarts != 1 {
		t.Errorf("%d restarts of incus.service, want 1", restarts)
	}
	st := d.srv.incusStatus()
	if st.Answering || st.Since == nil || st.Restarted == nil || !strings.Contains(st.Detail, "AgentBox restarted incus.service") {
		t.Errorf("status = %+v", st)
	}

	// Until the watch asks again, what needs Incus fails at once, with why,
	// and what doesn't need it answers.
	start := time.Now()
	_, err := d.client.Agents(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "Incus isn't answering") {
		t.Errorf("listing agents: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("listing agents took %s", took)
	}
	setup, err := d.client.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range setup.Checks {
		if c.ID == "incus" && !strings.Contains(c.Detail, "hasn't answered AgentBox since") {
			t.Errorf("Setup's Incus check = %+v", c)
		}
	}
	if err := d.client.Ping(ctx); err != nil {
		t.Errorf("the daemon doesn't answer: %v", err)
	}

	if plugged(t, d) {
		t.Fatal("the sockets were plugged in before Incus answered")
	}
	if !d.srv.checkIncus(ctx) {
		t.Fatal("an Incus answering again didn't have the sockets plugged in")
	}
	if st := d.srv.incusStatus(); !st.Answering || st.Detail != "" {
		t.Errorf("status = %+v, want answering", st)
	}
	if err := d.srv.cfg.Incus.Ping(ctx); err != nil {
		t.Errorf("Incus calls still fail: %v", err)
	}
	d.srv.plugSockets(ctx)
	if !plugged(t, d) {
		t.Error("the agent's socket wasn't plugged in again")
	}
	if d.srv.checkIncus(ctx) {
		t.Error("the sockets were plugged in again with nothing having changed")
	}
}

// A pass over the agents' sockets that couldn't ask a running agent is tried
// again at the watch's next answer.
func TestAgentSocketsAreTriedAgain(t *testing.T) {
	t.Parallel()
	d, _ := startWatchedIncus(t)
	ctx := context.Background()
	addTestAgent(t, d)
	execFail := filepath.Join(d.root, "exec-fail")
	mustWrite(t, execFail, "")

	if !d.srv.checkIncus(ctx) {
		t.Fatal("the daemon's first answer from Incus didn't have the sockets plugged in")
	}
	d.srv.plugSockets(ctx)
	if plugged(t, d) {
		t.Fatal("plugged in with the agent not answering")
	}
	if err := os.Remove(execFail); err != nil {
		t.Fatal(err)
	}
	if !d.srv.checkIncus(ctx) {
		t.Fatal("a failed pass wasn't tried again")
	}
	d.srv.plugSockets(ctx)
	if !plugged(t, d) {
		t.Error("the agent's socket wasn't plugged in on the second pass")
	}
}

// Outside the VM there is no restarting incus.service: the status says how.
func TestIncusWatchOutsideTheVMSaysHowToRestart(t *testing.T) {
	t.Parallel()
	d, hang := startWatchedIncus(t)
	mustWrite(t, hang, "")
	d.srv.incus.restart = nil
	d.srv.checkIncus(context.Background())
	if st := d.srv.incusStatus(); !strings.Contains(st.Detail, "sudo systemctl restart incus.service") {
		t.Errorf("detail = %q", st.Detail)
	}
}

func TestParseIncusUnit(t *testing.T) {
	u, err := parseIncusUnit("MainPID=0\nActiveState=activating\nSubState=start-post\n")
	if err != nil || u.MainPID != 0 || u.State != "activating/start-post" {
		t.Errorf("= %+v, %v", u, err)
	}
	if _, err := parseIncusUnit("nothing"); err == nil {
		t.Error("parsed nothing")
	}
}

func mustWrite(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
