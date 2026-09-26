package agent_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// loggingIncus is fakeIncus that also writes down every command it was given.
// Limits are nothing but incus calls, so what a change really did is exactly
// the list of commands it ran. (recordingIncus, next door, keeps the files
// AgentBox writes *into* an agent instead.)
func loggingIncus(t *testing.T, script string) (incus.Client, func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	path := filepath.Join(dir, "incus")
	body := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do printf '%%s ' \"$arg\" >> %q; done\nprintf '\\n' >> %q\n%s", log, log, script)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return incus.Client{Bin: path}, func() []string {
		t.Helper()
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var calls []string
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				calls = append(calls, line)
			}
		}
		return calls
	}
}

// oneCall finds the single call starting with prefix, and fails when there
// isn't exactly one: a limit applied twice is as wrong as one never applied.
func oneCall(t *testing.T, calls []string, prefix string) string {
	t.Helper()
	var found []string
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			found = append(found, call)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %q call, got %d:\n  %s", prefix, len(found), strings.Join(calls, "\n  "))
	}
	return found[0]
}

func noCall(t *testing.T, calls []string, substring string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call, substring) {
			t.Errorf("no call should mention %q, but one did: %s", substring, call)
		}
	}
}

// createScript answers the incus calls a Create makes: the base image is
// ready, the new instance has no configuration or devices of its own, and
// every agent-NN gets an address so WaitReady finishes.
const createScript = `case "$1" in
  list) echo '[
    {"name":"ab-hello-stack-agent-01","status":"Running","config":{},"expanded_config":{},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}},"memory":{"usage":1073741824}}},
    {"name":"ab-hello-stack-agent-02","status":"Running","config":{},"expanded_config":{},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}},"memory":{"usage":1073741824}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots*) echo '[]' ;;
      *) echo '{"config": {}, "expanded_config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`

// TestDefaultLimitsIsTwoCores checks the rule a fresh installation is seeded
// with: two cores, never more than the host actually has.
func TestDefaultLimitsIsTwoCores(t *testing.T) {
	for _, c := range []struct {
		host int
		want string
	}{
		{32, "2"}, {16, "2"}, {8, "2"}, {4, "2"}, {3, "2"}, {2, "2"}, {1, "1"}, {0, "2"},
	} {
		got := agent.DefaultLimits(c.host, 32<<30)
		if got.CPU != c.want {
			t.Errorf("DefaultLimits(%d cores).CPU = %q, want %q", c.host, got.CPU, c.want)
		}
		// No share: one below 100% would slow an agent down on an idle host.
		if got.Allowance != "" {
			t.Errorf("DefaultLimits(%d cores) = %+v, want no CPU share", c.host, got)
		}
	}
}

// TestDefaultMemoryIsEightGiBAtMostHalf checks the memory ceiling a fresh
// installation is seeded with: 8GiB, never more than half the host, and a
// size Incus takes.
func TestDefaultMemoryIsEightGiBAtMostHalf(t *testing.T) {
	const gib, mib = int64(1) << 30, int64(1) << 20
	for _, c := range []struct {
		host int64
		want string
	}{
		{64 * gib, "8GiB"},
		{16 * gib, "8GiB"},
		{30*gib + 600*mib, "8GiB"}, // the 30 GB machine six kind clusters froze
		{15*gib + 500*mib, "7GiB"}, // a "16 GB" laptop's MemTotal: half, rounded down
		{8 * gib, "4GiB"},
		{7*gib + 700*mib, "3GiB"},
		{2 * gib, "1GiB"},
		{1536 * mib, "768MiB"},
		{1000 * mib, "256MiB"},
		{300 * mib, "256MiB"}, // never nothing at all
		{0, "8GiB"},           // unreadable: the ordinary default
	} {
		got := agent.DefaultMemory(c.host)
		if got != c.want {
			t.Errorf("DefaultMemory(%s) = %q, want %q", agent.HumanBytes(c.host), got, c.want)
		}
		if err := agent.ValidateMemory(got); err != nil {
			t.Errorf("DefaultMemory(%s) = %q, which isn't valid: %v", agent.HumanBytes(c.host), got, err)
		}
		if n, _ := agent.ParseBytes(got); c.host > 512*mib && n > c.host/2 {
			t.Errorf("DefaultMemory(%s) = %q, more than half the host", agent.HumanBytes(c.host), got)
		}
	}
	if got := agent.DefaultLimits(8, 30*gib).Memory; got != "8GiB" {
		t.Errorf("DefaultLimits(8 cores, 30 GiB).Memory = %q, want 8GiB", got)
	}
}

// TestDefaultsAreWhatWasChosen checks how the installation's defaults resolve:
// a setting nobody has ever touched falls back to the host's core count, one
// stored as "" is a real choice (no limit), and one with a value is used.
func TestDefaultsAreWhatWasChosen(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	ctx := context.Background()

	// Nothing stored: the rule, not an empty limit.
	got, err := f.m.Defaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := agent.DefaultLimits(agent.HostCores(), agent.HostMemory()); got != want {
		t.Errorf("Defaults() on a fresh installation = %+v, want %+v", got, want)
	}

	// Stored as "": chosen, and it means no limit at all. This is the case
	// that can't be told from "nobody chose" without SettingValue.
	if err := f.st.SetSetting(ctx, state.SettingDefaultCPU, ""); err != nil {
		t.Fatal(err)
	}
	if got, err = f.m.Defaults(ctx); err != nil || got.CPU != "" {
		t.Errorf("Defaults() after choosing no CPU limit = %+v, %v", got, err)
	}

	for _, c := range []struct{ key, value string }{
		{state.SettingDefaultCPU, "6"},
		{state.SettingDefaultCPUAllowance, "50%"},
		{state.SettingDefaultMemory, "8GiB"},
	} {
		if err := f.st.SetSetting(ctx, c.key, c.value); err != nil {
			t.Fatal(err)
		}
	}
	got, err = f.m.Defaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := (agent.Limits{CPU: "6", Allowance: "50%", Memory: "8GiB"}); got != want {
		t.Errorf("Defaults() = %+v, want %+v", got, want)
	}
}

// TestCreateCapsNewAgents checks a new agent is capped without anyone asking:
// the installation's defaults, plus the CPU priority that makes the agent, and
// not the desktop, lose a contended host.
func TestCreateCapsNewAgents(t *testing.T) {
	inc, calls := loggingIncus(t, createScript)
	f := setup(t, inc)
	ctx := context.Background()
	for _, c := range []struct{ key, value string }{
		{state.SettingDefaultCPU, "6"},
		{state.SettingDefaultMemory, "8GiB"},
	} {
		if err := f.st.SetSetting(ctx, c.key, c.value); err != nil {
			t.Fatal(err)
		}
	}

	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	got := oneCall(t, calls(), "config set "+a.Instance+" limits.")
	want := "config set " + a.Instance + " limits.cpu=6 limits.memory=8GiB limits.cpu.priority=" + agent.CPUPriority + " limits.memory.swap=" + agent.MemorySwap
	if got != want {
		t.Errorf("limits applied at creation:\n got %s\nwant %s", got, want)
	}
	// Set before the machine starts, so the first `npm ci` inside it is
	// already capped rather than capped a moment later.
	var setAt, startAt int
	for i, call := range calls() {
		switch {
		case strings.HasPrefix(call, "config set "+a.Instance+" limits."):
			setAt = i
		case strings.HasPrefix(call, "start "+a.Instance):
			startAt = i
		}
	}
	if setAt > startAt {
		t.Errorf("limits were set after the machine started (%d > %d)", setAt, startAt)
	}
}

// TestCreateTakesTheLimitsChosenForOneAgent checks the per-agent choice, and
// the case the pointers exist for: an explicit empty CPU limit is a choice
// (every core), not a fallback to the installation's six, and an explicit
// empty memory limit is no ceiling — and so no reason to keep it out of swap.
func TestCreateTakesTheLimitsChosenForOneAgent(t *testing.T) {
	inc, calls := loggingIncus(t, createScript)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.st.SetSetting(ctx, state.SettingDefaultCPU, "6"); err != nil {
		t.Fatal(err)
	}
	unlimited, allowance := "", "50%"

	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{
		AI:     "none",
		Limits: agent.LimitChoice{CPU: &unlimited, Allowance: &allowance, Memory: &unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := oneCall(t, calls(), "config set "+a.Instance+" limits.")
	want := "config set " + a.Instance + " limits.cpu.allowance=50% limits.cpu.priority=" + agent.CPUPriority
	if got != want {
		t.Errorf("limits applied at creation:\n got %s\nwant %s", got, want)
	}
	noCall(t, calls(), "limits.cpu=")
	noCall(t, calls(), "limits.memory")
}

// TestCreateRefusesLimitsThatDontMeanWhatTheySay checks the values refused
// before a machine is copied, rather than by Incus afterwards.
func TestCreateRefusesLimitsThatDontMeanWhatTheySay(t *testing.T) {
	f := setup(t, fakeIncus(t, createScript))
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		choice agent.LimitChoice
		want   string
	}{
		{"a pinned range", agent.LimitChoice{CPU: ptr("0-3")}, "pin the agent to those exact cores"},
		{"a pinned set", agent.LimitChoice{CPU: ptr("1,2,3")}, "pin the agent to those exact cores"},
		{"half a core", agent.LimitChoice{CPU: ptr("0.5")}, "whole number of cores"},
		{"no cores", agent.LimitChoice{CPU: ptr("0")}, "whole number of cores"},
		{"a share over 100%", agent.LimitChoice{Allowance: ptr("150%")}, "between 1% and 100%"},
		{"a share that is neither", agent.LimitChoice{Allowance: ptr("half")}, "a time chunk like 25ms/100ms"},
		{"memory in words", agent.LimitChoice{Memory: ptr("lots")}, "a size like 8GiB"},
		{"negative memory", agent.LimitChoice{Memory: ptr("-2GiB")}, "a size like 8GiB"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none", Limits: c.choice})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Create(%+v) error = %v, want one mentioning %q", c.choice, err, c.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// TestSetLimitsAppliesThemLive checks what changing a running agent's limits
// costs: one `incus config set` for what it now has, one `incus config unset`
// per cap removed, and no restart. An empty value can't be set — Incus
// refuses limits.cpu="" — so removing a cap has to be an unset.
func TestSetLimitsAppliesThemLive(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"memory":{"usage":1073741824}}}]' ;;
  query) echo '{"config":{"limits.cpu":"4","limits.memory":"8GiB","limits.cpu.priority":"5"},"expanded_config":{"limits.cpu":"4","limits.memory":"8GiB","limits.cpu.priority":"5"},"devices":{}}' ;;
esac
exit 0`)
	f := setup(t, inc)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}

	got, err := f.m.SetLimits(context.Background(), a, agent.LimitChoice{CPU: ptr("12"), Memory: ptr(""), Allowance: ptr("50%")})
	if err != nil {
		t.Fatal(err)
	}
	if want := (agent.Limits{CPU: "12", Allowance: "50%", ConfiguredCPU: "12"}); got != want {
		t.Errorf("SetLimits() = %+v, want %+v", got, want)
	}
	sets := setCalls(t, calls())
	if sets[0] != "config set ab-hello-stack-agent-01 limits.cpu=12 limits.cpu.allowance=50%" {
		t.Errorf("the limits set call was %s", sets[0])
	}
	// A CPU choice made here is mirrored, so ConfiguredCPU still answers 12
	// once "never freeze my CPU" has moved limits.cpu somewhere else.
	if sets[1] != "config set ab-hello-stack-agent-01 user.agentbox.cpu.configured=12" {
		t.Errorf("the mirror set call was %s", sets[1])
	}
	if call := oneCall(t, calls(), "config unset "); call != "config unset ab-hello-stack-agent-01 limits.memory" {
		t.Errorf("the unset call was %s", call)
	}
	noCall(t, calls(), "restart")
	noCall(t, calls(), "stop ")
	// Already at 5: nothing to rewrite.
	noCall(t, calls(), "limits.cpu.priority")
}

// setCalls is oneCall for a prefix that may legitimately match more than
// once, in the order the calls were made.
func setCalls(t *testing.T, calls []string) []string {
	t.Helper()
	var found []string
	for _, call := range calls {
		if strings.HasPrefix(call, "config set ") {
			found = append(found, call)
		}
	}
	return found
}

// TestSetLimitsKeepsACappedAgentOutOfSwap checks limits.memory.swap follows
// the memory limit on a live agent: a ceiling brings swap off with it, since
// memory.max alone would let the agent go on into the host's swap, and
// removing the ceiling gives swap back.
func TestSetLimitsKeepsACappedAgentOutOfSwap(t *testing.T) {
	for _, c := range []struct {
		name, config, memory, want string
	}{
		{"capped", `{}`, "4GiB", "set ab-hello-stack-agent-01 limits.memory=4GiB limits.cpu.priority=5 limits.memory.swap=false"},
		{"uncapped", `{"limits.memory":"8GiB","limits.cpu.priority":"5","limits.memory.swap":"false"}`, "", "unset ab-hello-stack-agent-01 limits.memory.swap"},
	} {
		t.Run(c.name, func(t *testing.T) {
			inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  query) echo '{"config":`+c.config+`,"expanded_config":`+c.config+`,"devices":{}}' ;;
esac
exit 0`)
			f := setup(t, inc)
			a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}
			if _, err := f.m.SetLimits(context.Background(), a, agent.LimitChoice{Memory: ptr(c.memory)}); err != nil {
				t.Fatal(err)
			}
			oneCall(t, calls(), "config "+c.want)
		})
	}
}

// TestSetLimitsGivesAnOlderAgentAPriority checks a machine made before
// AgentBox capped anything is brought behind the desktop as soon as its limits
// are edited, rather than waiting for a rebuild.
func TestSetLimitsGivesAnOlderAgentAPriority(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  query) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
esac
exit 0`)
	f := setup(t, inc)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}

	if _, err := f.m.SetLimits(context.Background(), a, agent.LimitChoice{CPU: ptr("4")}); err != nil {
		t.Fatal(err)
	}
	sets := setCalls(t, calls())
	want := "config set ab-hello-stack-agent-01 limits.cpu=4 limits.cpu.priority=" + agent.CPUPriority
	if sets[0] != want {
		t.Errorf("the limits set call was %s, want %s", sets[0], want)
	}
	if want := "config set ab-hello-stack-agent-01 user.agentbox.cpu.configured=4"; sets[1] != want {
		t.Errorf("the mirror set call was %s, want %s", sets[1], want)
	}
	// Nothing was capped before, so there is nothing to unset.
	noCall(t, calls(), "config unset")
}

// TestSetLimitsRefusesMemoryBelowWhatTheAgentUses is the guard that exists
// because Incus would take this. limits.memory is live-updatable and goes
// straight into the cgroup's memory.max; the kernel then reclaims what it can
// and, failing that, invokes the OOM killer inside the agent. So the command
// would succeed and the agent's build would die with nothing to connect the
// two. A stopped agent has nothing running to lose, and is allowed.
func TestSetLimitsRefusesMemoryBelowWhatTheAgentUses(t *testing.T) {
	const usage = 5 * 1024 * 1024 * 1024 // 5 GiB, as `incus list` reports it
	running, calls := loggingIncus(t, fmt.Sprintf(`case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"memory":{"usage":%d}}}]' ;;
  query) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
esac
exit 0`, usage))
	f := setup(t, running)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}
	ctx := context.Background()

	_, err := f.m.SetLimits(ctx, a, agent.LimitChoice{Memory: ptr("2GiB")})
	if err == nil {
		t.Fatal("SetLimits() took a memory limit below what the agent is using")
	}
	for _, want := range []string{"is using 5.0 GiB", "kill processes inside the agent", "agentbox stop hello-stack/agent-01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal doesn't mention %q: %v", want, err)
		}
	}
	noCall(t, calls(), "config set")

	// Above what it uses is fine, and so is removing the ceiling altogether.
	if _, err := f.m.SetLimits(ctx, a, agent.LimitChoice{Memory: ptr("8GiB")}); err != nil {
		t.Errorf("a limit above what the agent uses: %v", err)
	}
	if _, err := f.m.SetLimits(ctx, a, agent.LimitChoice{Memory: ptr("")}); err != nil {
		t.Errorf("removing the ceiling: %v", err)
	}

	// Stopped: nothing is running to be killed, and the limit applies at its
	// next start.
	stopped, _ := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  query) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
esac
exit 0`)
	f.m.Incus = stopped
	if _, err := f.m.SetLimits(ctx, a, agent.LimitChoice{Memory: ptr("1GiB")}); err != nil {
		t.Errorf("a stopped agent should take any limit: %v", err)
	}
}

// TestForkInheritsTheSourceLimits checks a fork is capped like the agent it
// came from, not like a brand new agent: it is the same work on a copy of the
// same machine, so an agent someone had given more room keeps it.
func TestForkInheritsTheSourceLimits(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[
    {"name":"ab-hello-stack-agent-01","status":"Running","config":{"limits.cpu":"12"},"expanded_config":{"limits.cpu":"12","limits.memory":"16GiB"},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}},"memory":{"usage":1073741824}}},
    {"name":"ab-hello-stack-agent-02","status":"Running","config":{},"expanded_config":{},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}},"memory":{"usage":1073741824}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots*) echo '[]' ;;
      */ab-hello-stack-agent-01) echo '{"config":{"limits.cpu":"12"},"expanded_config":{"limits.cpu":"12","limits.memory":"16GiB"},"devices":{}}' ;;
      *) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
    esac ;;
esac
exit 0`)
	f := setup(t, inc)
	ctx := context.Background()
	// New agents are capped much lower: the fork must not take this instead.
	if err := f.st.SetSetting(ctx, state.SettingDefaultCPU, "2"); err != nil {
		t.Fatal(err)
	}
	src, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err != nil {
		t.Fatal(err)
	}

	fork, err := f.m.Fork(ctx, src, agent.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := oneCall(t, calls(), "config set "+fork.Instance+" limits.")
	want := "config set " + fork.Instance + " limits.cpu=12 limits.memory=16GiB limits.cpu.priority=" + agent.CPUPriority + " limits.memory.swap=" + agent.MemorySwap
	if got != want {
		t.Errorf("a fork's limits:\n got %s\nwant %s", got, want)
	}
}

// TestSaveBaseDoesNotBakeLimitsIn checks the scrub. `incus copy` copies
// configuration keys as well as devices, so without this the base would carry
// whatever the agent it was saved from was capped at, and every agent made
// from that base would inherit it — past the installation's defaults, and past
// any later change to them.
func TestSaveBaseDoesNotBakeLimitsIn(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.9"}]}}}}]' ;;
  query) echo '{"config":{"limits.cpu":"12","limits.memory":"16GiB","limits.cpu.priority":"5","limits.memory.swap":"false","user.agentbox.saved-from":"hello-stack/agent-01"},"expanded_config":{},"devices":{"worktree":{"type":"disk"}}}' ;;
esac
exit 0`)
	f := setup(t, inc)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}

	if _, err := f.m.SaveBase(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	const next = "ab-hello-stack-base-next"
	var unset []string
	for _, call := range calls() {
		if after, ok := strings.CutPrefix(call, "config unset "+next+" "); ok {
			unset = append(unset, after)
		}
	}
	want := []string{"limits.cpu", "limits.memory", "limits.cpu.priority", "limits.memory.swap"}
	if !slices.Equal(unset, want) {
		t.Errorf("the base was stripped of %v, want %v", unset, want)
	}
	// And the devices that belong to one agent still go, as they always did.
	oneCall(t, calls(), "config device remove "+next+" worktree")
}

// TestParseBytes checks the sizes a memory limit can be written in, since the
// refusal above compares one against what an agent is using.
func TestParseBytes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{
		{"8GiB", 8 << 30},
		{"4096MiB", 4096 << 20},
		{"512KiB", 512 << 10},
		{"2GB", 2_000_000_000},
		{"1024", 1024},
		{" 8GiB ", 8 << 30},
	} {
		got, err := agent.ParseBytes(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "lots", "8 gigs", "-1GiB", "0", "GiB"} {
		if _, err := agent.ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) was accepted", bad)
		}
	}
}
