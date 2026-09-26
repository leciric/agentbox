package agent_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// gpuQueryIncus is fakeIncus that answers "query" with an instance that
// already carries an agentbox-gpu device and nvidia.runtime=true, and
// "config device remove"/"config device add"/"config set" the way
// loggingIncus's underlying script does: it just needs to exist and exit
// clean, since the calls themselves are what's checked.
const gpuQueryIncus = `case "$1" in
  query) echo '{"config":{"nvidia.runtime":"true"},"devices":{"agentbox-gpu":{"type":"gpu"}}}' ;;
esac
exit 0
`

// gpuNoDeviceIncus answers "query" with no devices and no config at all.
const gpuNoDeviceIncus = `case "$1" in
  query) echo '{"config":{},"devices":{}}' ;;
esac
exit 0
`

func TestApplyGPUAddsTheDeviceOnlyWhenMissing(t *testing.T) {
	inc, calls := loggingIncus(t, gpuNoDeviceIncus)
	m := &agent.Manager{Incus: inc}
	status := agent.GPUStatus{Kind: agent.GPUAMD, Node: "/dev/dri/renderD128"}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", true, status); err != nil {
		t.Fatal(err)
	}
	oneCall(t, calls(), "config device add ab-p-a1 agentbox-gpu gpu mode=0666")
}

func TestApplyGPUAddsTheDeviceWithAModeEveryUserCanOpen(t *testing.T) {
	// mode=0666, not a gid, because a device added while the agent already
	// runs isn't handed to a group its shell was ever in — the same reason
	// android.go's /dev/kvm device uses mode=0666 instead of the kvm group.
	inc, calls := loggingIncus(t, gpuNoDeviceIncus)
	m := &agent.Manager{Incus: inc}
	status := agent.GPUStatus{Kind: agent.GPUAMD, Node: "/dev/dri/renderD128"}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", true, status); err != nil {
		t.Fatal(err)
	}
	call := oneCall(t, calls(), "config device add ab-p-a1 agentbox-gpu gpu")
	if !strings.Contains(call, "mode=0666") {
		t.Errorf("call = %q, want mode=0666", call)
	}
}

func TestApplyGPUSetsNvidiaRuntimeAsInstanceConfigNotADeviceProperty(t *testing.T) {
	inc, calls := loggingIncus(t, gpuNoDeviceIncus)
	m := &agent.Manager{Incus: inc}
	status := agent.GPUStatus{Kind: agent.GPUNvidia, Node: "/dev/nvidia0"}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", true, status); err != nil {
		t.Fatal(err)
	}
	device := oneCall(t, calls(), "config device add ab-p-a1 agentbox-gpu")
	if strings.Contains(device, "nvidia.runtime") {
		t.Errorf("nvidia.runtime is an instance config key, not a device property: %q", device)
	}
	oneCall(t, calls(), "config set ab-p-a1 nvidia.runtime=true")
}

func TestApplyGPUDoesNotSetNvidiaRuntimeOnAnAMDHost(t *testing.T) {
	inc, calls := loggingIncus(t, gpuNoDeviceIncus)
	m := &agent.Manager{Incus: inc}
	status := agent.GPUStatus{Kind: agent.GPUAMD, Node: "/dev/dri/renderD128"}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", true, status); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls() {
		if strings.Contains(call, "nvidia.runtime") {
			t.Errorf("no call should mention nvidia.runtime on an AMD host: %q", call)
		}
	}
}

func TestApplyGPULeavesAnExistingDeviceAndRuntimeFlagAlone(t *testing.T) {
	inc, calls := loggingIncus(t, gpuQueryIncus)
	m := &agent.Manager{Incus: inc}
	status := agent.GPUStatus{Kind: agent.GPUNvidia, Node: "/dev/nvidia0"}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", true, status); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls() {
		if strings.HasPrefix(call, "config") {
			t.Errorf("nothing should have run against an instance already matching on: %q", call)
		}
	}
}

func TestApplyGPURemovesTheDeviceWhenTurnedOff(t *testing.T) {
	inc, calls := loggingIncus(t, gpuQueryIncus)
	m := &agent.Manager{Incus: inc}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", false, agent.GPUStatus{}); err != nil {
		t.Fatal(err)
	}
	oneCall(t, calls(), "config device remove ab-p-a1 agentbox-gpu")
}

func TestApplyGPUClearsTheRuntimeFlagWhenTurnedOff(t *testing.T) {
	inc, calls := loggingIncus(t, gpuQueryIncus)
	m := &agent.Manager{Incus: inc}
	status := agent.GPUStatus{Kind: agent.GPUNvidia, Node: "/dev/nvidia0"}
	if err := m.ApplyGPU(context.Background(), "ab-p-a1", false, status); err != nil {
		t.Fatal(err)
	}
	oneCall(t, calls(), "config set ab-p-a1 nvidia.runtime=false")
}

// TestRecomputeGPURemovesTheDeviceFromEveryAgent exercises RecomputeGPU
// itself, end to end through the "GPU for agents" flag: this test's own
// machine has no GPU device (agent.HostGPU finds none in its container,
// which is exactly the case this feature is for), so RecomputeGPU always
// computes "off" here regardless of the flag, and the interesting behaviour
// left to check without real hardware is that it reaches every agent the
// store knows about, not only one.
func TestRecomputeGPURemovesTheDeviceFromEveryAgent(t *testing.T) {
	inc, calls := loggingIncus(t, gpuQueryIncus)
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
	if err := m.RecomputeGPU(ctx); err != nil {
		t.Fatal(err)
	}
	oneCall(t, calls(), "config device remove ab-p-a1 agentbox-gpu")
	oneCall(t, calls(), "config device remove ab-p-a2 agentbox-gpu")
}

// TestCreateSkipsTheGPUDeviceWithoutAHostGPU checks that turning "GPU for
// agents" on doesn't break Create, or add the device, on a machine with none
// to give — this test's own container, which is exactly why this feature
// exists. The branch that does add it at Create time (agent.go's build,
// gated on agent.HostGPU actually finding one) has no coverage from this
// test: it needs a real GPU device or a fake one at the paths HostGPU checks,
// neither of which this package can safely fake for an external test without
// reaching into agent's own unexported vars. ApplyGPU and gpuDeviceArgs,
// tested above, cover the device-adding logic itself in isolation.
func TestCreateSkipsTheGPUDeviceWithoutAHostGPU(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.st.SetFlag(ctx, state.SettingGPUForAgents, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"}); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls() {
		if strings.Contains(call, "agentbox-gpu") {
			t.Errorf("a host with no GPU shouldn't add the device: %q", call)
		}
	}
}
