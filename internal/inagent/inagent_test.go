package inagent

import (
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/api"
)

// machineOfItsOwn makes Inside look at nothing that is there: the tests may
// run on an agent themselves, whose marker, socket and hostname are all real.
func machineOfItsOwn(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(MarkerEnv, filepath.Join(dir, "no-marker"))
	t.Setenv(SocketEnv, filepath.Join(dir, "no.sock"))
	t.Setenv(HostnameEnv, "front-end-host")
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInsideIsFalseOnAMachineOfItsOwn(t *testing.T) {
	machineOfItsOwn(t)
	if Inside() {
		t.Error("Inside() is true with no marker, no socket and a hostname of its own")
	}
}

func TestInsideReadsEachSignal(t *testing.T) {
	for name, set := range map[string]func(t *testing.T){
		"the image's marker": func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "agentbox-image")
			touch(t, marker)
			t.Setenv(MarkerEnv, marker)
		},
		"the in-agent API socket": func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "agentbox.sock")
			touch(t, socket)
			t.Setenv(SocketEnv, socket)
		},
		// An agent made before the marker existed, with its socket not up
		// yet: Incus names the container ab-<project>-<agent>.
		"the container's hostname": func(t *testing.T) {
			t.Setenv(HostnameEnv, "ab-hello-stack-agent-01")
		},
	} {
		t.Run(name, func(t *testing.T) {
			machineOfItsOwn(t)
			set(t)
			if !Inside() {
				t.Errorf("Inside() is false with %s", name)
			}
		})
	}
}

func TestInsideIgnoresAHostnameThatMerelyContainsThePrefix(t *testing.T) {
	machineOfItsOwn(t)
	t.Setenv(HostnameEnv, "my-ab-laptop")
	if Inside() {
		t.Error("Inside() is true for a hostname that doesn't start with ab-")
	}
}

func TestMarkerAndSocketDefaultToTheAgentImagesPaths(t *testing.T) {
	t.Setenv(MarkerEnv, "")
	t.Setenv(SocketEnv, "")
	if got := Marker(); got != api.InAgentMarker {
		t.Errorf("Marker() = %q, want %q", got, api.InAgentMarker)
	}
	if got := Socket(); got != api.InAgentSocket {
		t.Errorf("Socket() = %q, want %q", got, api.InAgentSocket)
	}
}
