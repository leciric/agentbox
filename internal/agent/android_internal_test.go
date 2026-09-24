package agent

import (
	"runtime"
	"strings"
	"testing"

	"agentbox/internal/hostos"
)

// In AgentBox's VM on a Mac there is no KVM to run emulators with, whatever
// is installed, and the reason says so rather than pointing at firmware.
func TestAndroidUnsupportedInAMacsVM(t *testing.T) {
	t.Setenv(hostos.Env, "darwin")
	err := AndroidUnsupported()
	if err == nil || !strings.Contains(err.Error(), "VM on a Mac") {
		t.Fatalf("got %v", err)
	}
	t.Setenv(hostos.Env, "")
	if err := AndroidUnsupported(); (err == nil) != (runtime.GOARCH == "amd64") {
		t.Fatalf("on %s, not in a VM: got %v", runtime.GOARCH, err)
	}
}
