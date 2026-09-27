package agent_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

func repeat(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestAllocateCPU(t *testing.T) {
	tests := []struct {
		name     string
		budget   int
		ceilings []int
		want     []int
	}{
		{"no agents", 14, nil, nil},
		{"seven agents at the ceiling of two, fit in fourteen", 14, repeat(7, 2), repeat(7, 2)},
		{"an eighth leaves six agents with two and two with one", 14, repeat(8, 2),
			append(repeat(6, 2), repeat(2, 1)...)},
		{"nine agents leaves five with two and four with one", 14, repeat(9, 2),
			append(repeat(5, 2), repeat(4, 1)...)},
		{"one unlimited agent gets the whole budget", 15, []int{15}, []int{15}},
		{"two unlimited agents split into seven and eight", 15, []int{15, 15}, []int{7, 8}},
		{"more agents than the budget: everyone gets one", 4, repeat(6, 2), repeat(6, 1)},
		{"a ceiling below the budget is never raised to fill it", 14, []int{1, 1}, []int{1, 1}},
		{"a smaller ceiling is never taken below itself to spare a larger one",
			5, []int{1, 8}, []int{1, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agent.AllocateCPU(tt.budget, tt.ceilings)
			gotSorted, wantSorted := slices.Clone(got), slices.Clone(tt.want)
			slices.Sort(gotSorted)
			slices.Sort(wantSorted)
			if !slices.Equal(gotSorted, wantSorted) {
				t.Fatalf("AllocateCPU(%d, %v) = %v, want %v", tt.budget, tt.ceilings, got, tt.want)
			}
			total := 0
			for _, c := range got {
				total += c
			}
			if total > tt.budget && len(got) > 0 && total > len(got) {
				t.Fatalf("AllocateCPU(%d, %v) = %v sums to %d, over budget", tt.budget, tt.ceilings, got, total)
			}
		})
	}
}

// recomputeCPUCapsIncus answers `list` with two running agents, ab-p-a1 and
// ab-p-a2, each already chosen to run on 4 cores (user.agentbox.cpu.
// configured) but with no limits.cpu of its own yet, so RecomputeCPUCaps
// always has something to restore for both; `config set` fails for
// failInstance, the way a transient Incus error would for one agent among
// several, and succeeds (silently, like the rest of this package's fakes)
// for everything else.
func recomputeCPUCapsIncus(t *testing.T, failInstance string) (incus.Client, func() []string) {
	t.Helper()
	return loggingIncus(t, fmt.Sprintf(`case "$1 $2 $3" in
  "config set %s") echo boom >&2; exit 1 ;;
esac
case "$1" in
  list) echo '[
    {"name":"ab-p-a1","status":"Running","config":{"user.agentbox.cpu.configured":"4"},"expanded_config":{"user.agentbox.cpu.configured":"4"},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},
    {"name":"ab-p-a2","status":"Running","config":{"user.agentbox.cpu.configured":"4"},"expanded_config":{"user.agentbox.cpu.configured":"4"},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]' ;;
  query) echo '{"config":{},"devices":{}}' ;;
esac
exit 0
`, failInstance))
}

// TestRecomputeCPUCapsSkipsTheLead checks that a lead sitting in the store
// alongside ordinary agents doesn't stop RecomputeCPUCaps: unlike GPU, this
// one is already driven off Incus's own instance list rather than looping
// over agents and reaching for Incus directly, so a lead — which is never in
// that list, having no machine of its own (Agent.Role) — is naturally left
// alone. This pins that down so a future rewrite doesn't quietly start
// looking the lead's instance up.
func TestRecomputeCPUCapsSkipsTheLead(t *testing.T) {
	inc, calls := recomputeCPUCapsIncus(t, "")
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	lead := state.Agent{
		Project: "p", Name: state.LeadName, Instance: "ab-p-lead", AI: "claude",
		Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(), Role: state.RoleLead,
	}
	if err := st.AddAgent(ctx, lead); err != nil {
		t.Fatal(err)
	}
	worker := state.Agent{
		Project: "p", Name: "a1", Instance: "ab-p-a1", AI: "none",
		Branch: "agentbox/a1", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := st.AddAgent(ctx, worker); err != nil {
		t.Fatal(err)
	}
	m := &agent.Manager{Incus: inc, Store: st}
	if err := m.RecomputeCPUCaps(ctx); err != nil {
		t.Fatalf("RecomputeCPUCaps() with a lead in the store = %v, want nil", err)
	}
	oneCall(t, calls(), "config set ab-p-a1")
	noCall(t, calls(), "ab-p-lead")
}

// TestRecomputeCPUCapsContinuesPastAFailingInstance checks that one agent
// whose Incus call fails doesn't stop RecomputeCPUCaps from reaching the
// rest: it used to return on the very first error, leaving every agent after
// the broken one with whatever limits it already had.
func TestRecomputeCPUCapsContinuesPastAFailingInstance(t *testing.T) {
	inc, calls := recomputeCPUCapsIncus(t, "ab-p-a2")
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a1", "a2"} {
		a := state.Agent{
			Project: "p", Name: name, Instance: "ab-p-" + name, AI: "none",
			Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
		}
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	m := &agent.Manager{Incus: inc, Store: st}
	if err := m.RecomputeCPUCaps(ctx); err == nil {
		t.Error("RecomputeCPUCaps() = nil, want ab-p-a2's error reported")
	}
	oneCall(t, calls(), "config set ab-p-a1")
}
