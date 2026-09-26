package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// cpuBudgetIncus is fakeIncus with enough behind `stop` and `start` to matter
// here: each flips the instance's own status in $INCUS_INSTANCES_FILE, the
// way the real Incus would, so a second `list` — the one RecomputeCPUCaps
// makes right after — sees the agent's new state rather than the one it
// started the request in.
const cpuBudgetIncus = `case "$1" in
  list) cat "$INCUS_INSTANCES_FILE" ;;
  query) echo '{"config":{},"devices":{}}' ;;
  config) echo "$*" >> "$INCUS_LOG" ;;
  stop) sed -i "s/\"name\":\"$2\",\"status\":\"[A-Za-z]*\"/\"name\":\"$2\",\"status\":\"Stopped\"/" "$INCUS_INSTANCES_FILE" ;;
  start) sed -i "s/\"name\":\"$2\",\"status\":\"[A-Za-z]*\"/\"name\":\"$2\",\"status\":\"Running\"/" "$INCUS_INSTANCES_FILE" ;;
esac
exit 0
`

// cpuBudgetInstances is two agents, both already chosen (by an earlier
// SetLimits or Create — ConfiguredCPU's mirror) to run on `ceiling` cores.
func cpuBudgetInstances(ceiling int, a1status, a2status string) string {
	instance := func(name, status, addr string) string {
		return fmt.Sprintf(`{"name":%q,"status":%q,"config":{"user.agentbox.cpu.configured":%q},"expanded_config":{"user.agentbox.cpu.configured":%q},`+
			`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":%q}]}}}}`,
			name, status, strconv.Itoa(ceiling), strconv.Itoa(ceiling), addr)
	}
	return "[" + instance("ab-p-a1", a1status, "10.0.0.5") + "," + instance("ab-p-a2", a2status, "10.0.0.6") + "]"
}

// TestCPUCapsUpdateOnStartAndStop checks the sum of what "never freeze my
// CPU" allows to add up to changes live as agents leave and rejoin the
// running set — not only when the setting itself changes (D95). Both
// agents' ceiling is deliberately more than this host has, so the two of
// them are always oversubscribed against each other and the exact figures
// this test expects are agent.AllocateCPU's own answer, not numbers picked by
// hand — this only has to prove the daemon feeds it the right budget and
// ceilings on every transition, since AllocateCPU's own correctness is
// covered by TestAllocateCPU.
func TestCPUCapsUpdateOnStartAndStop(t *testing.T) {
	t.Parallel()
	budget := agent.HostCores() // keepFree 0
	ceiling := budget + 5
	d := startTestDaemon(t, t.TempDir(), cpuBudgetIncus, testConfig{instances: cpuBudgetInstances(ceiling, "Running", "Running")})
	ctx := context.Background()

	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a1", "a2"} {
		a := state.Agent{
			Project: "p", Name: name, Instance: "ab-p-" + name, AI: "none",
			Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
		}
		if err := d.srv.store.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	log := func() string {
		b, err := os.ReadFile(filepath.Join(d.root, "incus.log"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	clearLog := func() {
		if err := os.WriteFile(filepath.Join(d.root, "incus.log"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantSet := func(step, instance string, cpu int) {
		t.Helper()
		if want := fmt.Sprintf("config set %s limits.cpu=%d", instance, cpu); !strings.Contains(log(), want) {
			t.Errorf("%s: the log doesn't have %q:\n%s", step, want, log())
		}
	}

	both := agent.AllocateCPU(budget, []int{ceiling, ceiling})
	if _, err := patchSettings(t, d, `{"neverFreezeCPU":true,"keepFreeCPU":0}`); err != nil {
		t.Fatal(err)
	}
	wantSet("turning the setting on", "ab-p-a1", both[0])
	wantSet("turning the setting on", "ab-p-a2", both[1])

	clearLog()
	if _, err := d.client.AgentAction(ctx, "p/a2", "stop"); err != nil {
		t.Fatal(err)
	}
	alone := agent.AllocateCPU(budget, []int{ceiling})
	wantSet("stopping a2", "ab-p-a1", alone[0])
	if strings.Contains(log(), "ab-p-a2") {
		t.Errorf("a2 is stopped: nothing should have been applied to it\n%s", log())
	}

	clearLog()
	if _, err := d.client.AgentAction(ctx, "p/a2", "start"); err != nil {
		t.Fatal(err)
	}
	wantSet("starting a2 again", "ab-p-a1", both[0])
	wantSet("starting a2 again", "ab-p-a2", both[1])

	// Turned off: each goes back to what it was really chosen to be —
	// ceiling, not the budget's share of it.
	clearLog()
	if _, err := patchSettings(t, d, `{"neverFreezeCPU":false}`); err != nil {
		t.Fatal(err)
	}
	wantSet("turning the setting off", "ab-p-a1", ceiling)
	wantSet("turning the setting off", "ab-p-a2", ceiling)
}
