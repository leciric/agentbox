package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// runFixture is a manager whose incus runs `exec` scripts right here, as the
// machine would, and knows machines in each state the chat may meet.
func runFixture(t *testing.T) agent.Manager {
	t.Helper()
	inc := fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-one","status":"Running"},{"name":"ab-two","status":"Running"},{"name":"ab-off","status":"Stopped"},{"name":"ab-paused","status":"Frozen"}]' ;;
  exec) [ "$4 $5 $6 $7" = "runuser -l dev -c" ] || { echo "unexpected exec: $*" >&2; exit 99; }; exec bash -c "$8" ;;
  *) echo "unexpected: $*" >&2; exit 99 ;;
esac
`)
	f := setup(t, inc)
	return *f.m
}

func machine(t *testing.T, name, instance string) state.Agent {
	return state.Agent{Project: "hello-stack", Name: name, Instance: instance, Worktree: t.TempDir(), Status: state.AgentReady}
}

func TestRunForLeadKeepsTheTailAndCountsTheRest(t *testing.T) {
	m := runFixture(t)
	a := machine(t, "agent-01", "ab-one")
	if err := os.WriteFile(filepath.Join(a.Worktree, "marker"), []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	// It runs in the worktree, and reads nothing from stdin.
	res, err := m.RunForLead(context.Background(), a, "cat marker; echo; read x || echo no-stdin; echo oops >&2; exit 3", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.TimedOut || res.Output != "here\nno-stdin\noops\n" {
		t.Errorf("RunForLead() = %+v", res)
	}

	res, err = m.RunForLead(context.Background(), a, "seq 1 100000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || len(res.Output) > agent.LeadRunTail || !strings.HasSuffix(res.Output, "\n99999\n100000\n") {
		t.Errorf("RunForLead() kept %d bytes ending %q", len(res.Output), res.Output[max(0, len(res.Output)-20):])
	}
	// The tail starts at a line, and the total is all of it.
	if first, _, _ := strings.Cut(res.Output, "\n"); len(first) < 5 || strings.HasPrefix(first, "0") {
		t.Errorf("the tail starts mid-line: %q", first)
	}
	if res.Bytes != 588895 {
		t.Errorf("Bytes = %d, want 588895", res.Bytes)
	}
}

func TestRunForLeadStopsAtItsTimeout(t *testing.T) {
	m := runFixture(t)
	a := machine(t, "agent-01", "ab-one")
	start := time.Now()
	res, err := m.RunForLead(context.Background(), a, "echo started; sleep 30", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Output != "started\n" {
		t.Errorf("RunForLead() = %+v, want it timed out", res)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("a 1s timeout took %s", took)
	}
	if _, err := m.RunForLead(context.Background(), a, "true", agent.MaxLeadRunTimeout+time.Second); err == nil || !strings.Contains(err.Error(), "task for an agent") {
		t.Errorf("a timeout over the limit: %v", err)
	}
}

func TestRunForLeadRefusesWhatItCantRunIn(t *testing.T) {
	m := runFixture(t)
	lead := machine(t, "lead", "")
	lead.Role = state.RoleLead
	creating := machine(t, "agent-05", "ab-one")
	creating.Status = "creating"
	for _, tc := range []struct {
		a    state.Agent
		want string
	}{
		{lead, "the project's chat, which has no machine"},
		{machine(t, "agent-02", "ab-off"), "agent-02's machine is stopped (retired, or stopped by the user)"},
		{machine(t, "agent-03", "ab-paused"), "agent-03 is paused (retired, or paused by the user)"},
		{machine(t, "agent-04", "ab-gone"), "agent-04 has no machine"},
		{creating, "isn't ready yet"},
	} {
		if _, err := m.RunForLead(context.Background(), tc.a, "true", 0); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("RunForLead(%s) = %v, want %q", tc.a.Name, err, tc.want)
		}
	}
	if _, err := m.RunForLead(context.Background(), machine(t, "agent-01", "ab-one"), "  ", 0); err == nil {
		t.Error("an empty command ran")
	}
}

func TestCopyForLeadMovesFilesBetweenMachines(t *testing.T) {
	ctx := context.Background()
	m := runFixture(t)
	src, dst := machine(t, "agent-01", "ab-one"), machine(t, "agent-02", "ab-two")
	dir := filepath.Join(src.Worktree, "fixtures", "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	// Into the same place on the other side, by default.
	n, err := m.CopyForLead(ctx, src, "fixtures/data/", dst, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dst.Worktree, "fixtures", "data", "a.json")); err != nil || string(got) != `{"a":1}` || n == 0 {
		t.Errorf("copied %q, %v (%d bytes)", got, err, n)
	}
	// A symlink arrives as a symlink, not as what it pointed to.
	if target, err := os.Readlink(filepath.Join(dst.Worktree, "fixtures", "data", "link")); err != nil || target != "/etc/hostname" {
		t.Errorf("link = %q, %v", target, err)
	}
	// Or somewhere else.
	if _, err := m.CopyForLead(ctx, src, "fixtures/data/a.json", dst, "elsewhere/deep", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst.Worktree, "elsewhere", "deep", "a.json")); err != nil {
		t.Error(err)
	}

	for _, tc := range []struct {
		from     string
		src, dst state.Agent
		want     string
	}{
		{"missing.txt", src, dst, "reading missing.txt in agent-01"},
		{".", src, dst, "name the file or directory"},
		{"fixtures", src, src, "both ends of the copy"},
		{"fixtures", src, machine(t, "agent-03", "ab-paused"), "agent-03 is paused"},
	} {
		if _, err := m.CopyForLead(ctx, tc.src, tc.from, tc.dst, "", 0); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CopyForLead(%s) = %v, want %q", tc.from, err, tc.want)
		}
	}
}
