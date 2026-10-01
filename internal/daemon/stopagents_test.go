package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// statefulIncus is fakeIncus with enough behind `stop` and `start` to matter:
// each flips the instance's own status in $INCUS_INSTANCES_FILE, the way the
// real Incus would, so a later `list` sees the agent's new state rather than
// the one it started the request in.
const statefulIncus = `case "$1" in
  list) cat "$INCUS_INSTANCES_FILE" ;;
  query) echo '{"config":{},"devices":{}}' ;;
  config) echo "$*" >> "$INCUS_LOG" ;;
  stop) sed -i "s/\"name\":\"$2\",\"status\":\"[A-Za-z]*\"/\"name\":\"$2\",\"status\":\"Stopped\"/" "$INCUS_INSTANCES_FILE" ;;
  start) sed -i "s/\"name\":\"$2\",\"status\":\"[A-Za-z]*\"/\"name\":\"$2\",\"status\":\"Running\"/" "$INCUS_INSTANCES_FILE" ;;
esac
exit 0
`

// stopAgentsIncus is statefulIncus behind a lock: the job stops agents in
// parallel, and two of its `sed -i` on the one instances file would lose
// each other's change.
var stopAgentsIncus = "exec 9>\"$INCUS_INSTANCES_FILE.lock\"; flock 9\n" + statefulIncus

// stopAgentsDaemon is a daemon with three agents in two projects: p/a1
// running, p/a2 paused and q/b1 already stopped.
func stopAgentsDaemon(t *testing.T) testDaemon {
	t.Helper()
	instance := func(name, status string) string {
		return fmt.Sprintf(`{"name":%q,"status":%q,"config":{},"expanded_config":{}}`, name, status)
	}
	instances := "[" + instance("ab-p-a1", "Running") + "," + instance("ab-p-a2", "Frozen") + "," + instance("ab-q-b1", "Stopped") + "]"
	d := startTestDaemon(t, t.TempDir(), stopAgentsIncus, testConfig{instances: instances})
	ctx := context.Background()
	for _, p := range []string{"p", "q"} {
		if err := d.srv.store.AddProject(ctx, state.Project{Name: p, Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"p/a1", "p/a2", "q/b1"} {
		project, name, _ := strings.Cut(ref, "/")
		a := state.Agent{
			Project: project, Name: name, Title: "Agent " + name, Instance: "ab-" + project + "-" + name, AI: "none",
			Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
		}
		if err := d.srv.store.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func finishStopAgents(t *testing.T, d testDaemon, req api.StopAgentsRequest) (api.Job, api.StopAgentsResult) {
	t.Helper()
	ctx := context.Background()
	job, err := d.client.StopAgents(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "stop-agents" {
		t.Errorf("job kind = %q, want stop-agents", job.Kind)
	}
	waitFor(t, "the agents to stop", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	job, err = d.client.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result api.StopAgentsResult
	if job.Status == api.JobSucceeded {
		if err := json.Unmarshal(job.Result, &result); err != nil {
			t.Fatal(err)
		}
	}
	return job, result
}

// TestStopAgentsStopsEveryRunningAndPausedAgent checks "Free resources":
// every agent of every project that holds anything — running or paused — is
// stopped, the one already stopped is left alone, and the result names each.
func TestStopAgentsStopsEveryRunningAndPausedAgent(t *testing.T) {
	t.Parallel()
	d := stopAgentsDaemon(t)
	job, result := finishStopAgents(t, d, api.StopAgentsRequest{})
	if job.Status != api.JobSucceeded {
		t.Fatalf("stop-agents = %s: %s", job.Status, job.Error)
	}
	var refs []string
	for _, a := range result.Stopped {
		refs = append(refs, a.Ref)
	}
	if got := strings.Join(slices.Sorted(slices.Values(refs)), ","); got != "p/a1,p/a2" {
		t.Errorf("stopped = %s, want p/a1,p/a2", got)
	}
	if len(result.Failed) > 0 {
		t.Errorf("failed = %+v, want none", result.Failed)
	}
	log, err := os.ReadFile(filepath.Join(d.root, "incus.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "ab-q-b1") {
		t.Errorf("q/b1 was already stopped: nothing should have touched it\n%s", log)
	}
	for _, ref := range []string{"p/a1", "p/a2"} {
		a, err := d.client.Agent(context.Background(), ref)
		if err != nil {
			t.Fatal(err)
		}
		if a.State != "stopped" {
			t.Errorf("%s is %s after stopping every agent, want stopped", ref, a.State)
		}
	}
	text, err := d.client.JobLog(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Stopped Agent a1 (p/a1)") {
		t.Errorf("the log doesn't say a1 was stopped:\n%s", text)
	}
}

// TestStopAgentsOnlyRefs checks a request naming agents stops only those, and
// that naming one that doesn't exist is refused up front rather than
// quietly stopping nothing.
func TestStopAgentsOnlyRefs(t *testing.T) {
	t.Parallel()
	d := stopAgentsDaemon(t)
	job, result := finishStopAgents(t, d, api.StopAgentsRequest{Refs: []string{"p/a2"}})
	if job.Status != api.JobSucceeded {
		t.Fatalf("stop-agents = %s: %s", job.Status, job.Error)
	}
	if len(result.Stopped) != 1 || result.Stopped[0].Ref != "p/a2" {
		t.Errorf("stopped = %+v, want only p/a2", result.Stopped)
	}
	if a, err := d.client.Agent(context.Background(), "p/a1"); err != nil || a.State != "running" {
		t.Errorf("p/a1 = %s (%v), want still running", a.State, err)
	}
	if _, err := d.client.StopAgents(context.Background(), api.StopAgentsRequest{Refs: []string{"p/nope"}}); err == nil || !strings.Contains(err.Error(), "no agent p/nope") {
		t.Errorf("stopping an agent that doesn't exist = %v, want an error naming it", err)
	}
}
