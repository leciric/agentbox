package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// dockerPruneIncus is statefulIncus whose `exec` plays an agent with Docker:
// each command it runs goes to $INCUS_LOG, the check answers $DOCKER_STATE
// (running unless set), and the prunes answer what Docker prints, the image
// one failing when $IMAGE_PRUNE_FAILS is set. $DOCKER_HANGS makes the build
// cache prune never return.
const dockerPruneIncus = `case "$1" in
  list) cat "$INCUS_INSTANCES_FILE" ;;
  query) echo '{"config":{},"devices":{}}' ;;
  exec) shift 3
    echo "exec $1 $2 $3 $4" | head -n 1 >> "$INCUS_LOG"
    case "$1 $2" in
      "bash -c") echo "${DOCKER_STATE:-running}" ;;
      "docker builder") [ -n "$DOCKER_HANGS" ] && exec sleep 30
        printf 'ID\tRECLAIMABLE\tSIZE\nabc\ttrue\t7.05GB\nTotal:\t7.05GB\n' ;;
      "docker image") if [ -n "$IMAGE_PRUNE_FAILS" ]; then echo "Error: Docker went away" >&2; exit 1; fi
        printf 'Deleted Images:\ndeleted: sha256:abc\n\nTotal reclaimed space: 17.2GB\n' ;;
    esac ;;
  stop) echo "stop $2" >> "$INCUS_LOG"
    sed -i "s/\"name\":\"$2\",\"status\":\"[A-Za-z]*\"/\"name\":\"$2\",\"status\":\"Stopped\"/" "$INCUS_INSTANCES_FILE" ;;
esac
exit 0
`

// newDockerPruneTest is a daemon with one agent, p/a1, at the given status,
// its fake Docker set up by env, and the prune's timeout when one is given.
func newDockerPruneTest(t *testing.T, status string, env map[string]string, timeout ...time.Duration) (testDaemon, state.Agent) {
	t.Helper()
	instance := "ab-p-a1"
	tc := testConfig{env: env, instances: autoStopIdleInstances(instance, status)}
	if len(timeout) > 0 {
		tc.dockerPruneTimeout = timeout[0]
	}
	d := startTestDaemon(t, t.TempDir(), dockerPruneIncus, tc)
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "p", Name: "a1", Title: "Agent a1", Instance: instance, AI: "none",
		Branch: "agentbox/a1", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	return d, a
}

// dockerPruneLog is every line the fake incus logged.
func dockerPruneLog(t *testing.T, d testDaemon) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.root, "incus.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// dockerPrunedEvents are the docker_pruned events recorded for a1.
func dockerPrunedEvents(t *testing.T, d testDaemon) []api.AgentEvent {
	t.Helper()
	rows, err := d.srv.store.AgentEvents(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	var out []api.AgentEvent
	for _, row := range rows {
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) == nil && ev.Kind == api.AgentDockerPruned {
			out = append(out, ev)
		}
	}
	return out
}

func TestStopPrunesDocker(t *testing.T) {
	t.Parallel()
	d, a := newDockerPruneTest(t, "Running", nil)
	if _, err := d.client.AgentAction(context.Background(), a.Ref(), "stop"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec bash -c",
		"exec docker builder prune --all",
		"exec docker image prune --all",
		"stop ab-p-a1",
	}
	got := dockerPruneLog(t, d)
	if len(got) < len(want) {
		t.Fatalf("incus ran %q, want %q first", got, want)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("incus call %d = %q, want %q", i, got[i], w)
		}
	}
	for _, line := range got {
		if strings.Contains(line, "volume") || strings.Contains(line, "system") {
			t.Errorf("incus ran %q: a stop must never touch volumes", line)
		}
	}
	events := dockerPrunedEvents(t, d)
	if len(events) != 1 {
		t.Fatalf("docker_pruned events = %d, want 1", len(events))
	}
	// 17.2GB + 7.05GB, decimal, is 22.6 GiB.
	if want := "Freed 22.6 GiB of Docker images and build cache"; events[0].Summary != want {
		t.Errorf("summary = %q, want %q", events[0].Summary, want)
	}
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("instance = %q, want Stopped", got)
	}
}

// TestStopAgentsPrunesDocker checks every stop goes through the prune, here
// stopping every agent at once.
func TestStopAgentsPrunesDocker(t *testing.T) {
	t.Parallel()
	d, _ := newDockerPruneTest(t, "Running", nil)
	finishStopAgents(t, d, api.StopAgentsRequest{})
	if len(dockerPrunedEvents(t, d)) != 1 {
		t.Error("stopping every agent didn't prune its Docker")
	}
}

func TestStopDoesntPruneWhenOff(t *testing.T) {
	t.Parallel()
	d, a := newDockerPruneTest(t, "Running", nil)
	if _, err := d.client.UpdateSettings(context.Background(), api.UpdateSettingsRequest{DockerPruneOnStop: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AgentAction(context.Background(), a.Ref(), "stop"); err != nil {
		t.Fatal(err)
	}
	for _, line := range dockerPruneLog(t, d) {
		if strings.HasPrefix(line, "exec") {
			t.Errorf("incus ran %q with the setting off", line)
		}
	}
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("instance = %q, want Stopped", got)
	}
}

func TestStopSkipsPruneWithoutDocker(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"absent", "stopped"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			d, a := newDockerPruneTest(t, "Running", map[string]string{"DOCKER_STATE": state})
			if _, err := d.client.AgentAction(context.Background(), a.Ref(), "stop"); err != nil {
				t.Fatal(err)
			}
			for _, line := range dockerPruneLog(t, d) {
				if strings.HasPrefix(line, "exec docker") {
					t.Errorf("incus ran %q with Docker %s", line, state)
				}
			}
			if len(dockerPrunedEvents(t, d)) != 0 {
				t.Error("recorded a prune that never ran")
			}
			if got := instanceStatus(t, d); got != "Stopped" {
				t.Errorf("instance = %q, want Stopped", got)
			}
		})
	}
}

// A paused machine can't run anything: it's stopped as it is.
func TestStopSkipsPruneWhenPaused(t *testing.T) {
	t.Parallel()
	d, a := newDockerPruneTest(t, "Frozen", nil)
	if _, err := d.client.AgentAction(context.Background(), a.Ref(), "stop"); err != nil {
		t.Fatal(err)
	}
	for _, line := range dockerPruneLog(t, d) {
		if strings.HasPrefix(line, "exec") {
			t.Errorf("incus ran %q in a paused machine", line)
		}
	}
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("instance = %q, want Stopped", got)
	}
}

// A prune that fails part way still stops the agent, and still tells what
// it did free.
func TestStopPruneFailureDoesntBlock(t *testing.T) {
	t.Parallel()
	d, a := newDockerPruneTest(t, "Running", map[string]string{"IMAGE_PRUNE_FAILS": "1"})
	if _, err := d.client.AgentAction(context.Background(), a.Ref(), "stop"); err != nil {
		t.Fatal(err)
	}
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("instance = %q, want Stopped", got)
	}
	events := dockerPrunedEvents(t, d)
	if len(events) != 1 || !strings.Contains(events[0].Summary, "6.6 GiB") {
		t.Errorf("events = %+v, want the build cache's 6.6 GiB", events)
	}
}

// A Docker that hangs holds the stop up only as long as the timeout.
func TestStopPruneTimesOut(t *testing.T) {
	t.Parallel()
	d, a := newDockerPruneTest(t, "Running", map[string]string{"DOCKER_HANGS": "1"}, 300*time.Millisecond)
	start := time.Now()
	if _, err := d.client.AgentAction(context.Background(), a.Ref(), "stop"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("stop took %s with Docker hanging", took)
	}
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("instance = %q, want Stopped", got)
	}
	if len(dockerPrunedEvents(t, d)) != 0 {
		t.Error("recorded a prune that never finished")
	}
}

func TestDockerPruneSetting(t *testing.T) {
	t.Parallel()
	d, _ := newDockerPruneTest(t, "Running", nil)
	ctx := context.Background()
	s, err := d.client.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.DockerPruneOnStop {
		t.Error("DockerPruneOnStop is off by default, want on")
	}
	if s, err = d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{DockerPruneOnStop: ptr(false)}); err != nil || s.DockerPruneOnStop {
		t.Errorf("after turning it off: %v, %v", s.DockerPruneOnStop, err)
	}
}
