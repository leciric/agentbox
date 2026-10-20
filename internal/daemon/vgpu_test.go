package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// In a Mac's krunkit VM, turning "GPU for agents" on readies the VM's GPU,
// and Settings says how far that has got. Not parallel: it points package
// agent's globals at a venus.sh of its own.
func TestReadyVMGPU(t *testing.T) {
	dir := t.TempDir()
	oldScript, oldMarker, oldSudo := agent.VenusScript, agent.VenusRestartMarker, agent.VenusSudo
	t.Cleanup(func() { agent.VenusScript, agent.VenusRestartMarker, agent.VenusSudo = oldScript, oldMarker, oldSudo })
	agent.VenusScript, agent.VenusRestartMarker, agent.VenusSudo = filepath.Join(dir, "venus.sh"), filepath.Join(dir, "restart"), nil
	// It says it ran, waits for go, then fails with fail or leaves the
	// restart marker.
	script := `echo "retry=$AGENTBOX_VENUS_RETRY" >>` + dir + `/ran
while [ ! -e ` + dir + `/go ]; do sleep 0.01; done
if [ -e ` + dir + `/fail ]; then echo "GPU: it broke" >&2; exit 1; fi
touch ` + dir + `/restart
`
	if err := os.WriteFile(agent.VenusScript, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	setup := func() api.VMGPU {
		t.Helper()
		out, err := d.srv.currentSettings(httptest.NewRequest(http.MethodGet, "/v1/settings", nil))
		if err != nil {
			t.Fatal(err)
		}
		return out.VMGPU
	}

	// Off, nothing runs, and Settings offers it.
	if got := setup(); got.Kind != "" || got.Setup != api.VMGPUSetupAvailable {
		t.Errorf("before: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Error("venus.sh ran with GPU for agents off")
	}

	_ = os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	if _, err := patchSettings(t, d, `{"agentGPU":true}`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for setup().Setup != api.VMGPUSetupRunning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := setup(); got.Setup != api.VMGPUSetupRunning {
		t.Errorf("while it runs: %+v", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "go"), nil, 0o644)
	d.srv.vgpu.wg.Wait()
	if got := setup(); got.Setup != api.VMGPUSetupFailed || got.Reason != "GPU: it broke" {
		t.Errorf("failed: %+v", got)
	}

	// Turned off and on, it tries again; the daemon starting tries without
	// retrying a kernel that didn't boot.
	_ = os.Remove(filepath.Join(dir, "fail"))
	for _, body := range []string{`{"agentGPU":false}`, `{"agentGPU":true}`} {
		if _, err := patchSettings(t, d, body); err != nil {
			t.Fatal(err)
		}
	}
	d.srv.vgpu.wg.Wait()
	if got := setup(); got.Setup != api.VMGPUSetupRestart {
		t.Errorf("waiting for a restart: %+v", got)
	}
	d.srv.readyVMGPU(t.Context(), false)
	d.srv.vgpu.wg.Wait()
	ran, _ := os.ReadFile(filepath.Join(dir, "ran"))
	if got := strings.Fields(string(ran)); strings.Join(got, " ") != "retry=1 retry=1 retry=0" {
		t.Errorf("ran %q", got)
	}
}
