package cli

import (
	"bytes"
	"testing"

	"agentbox/internal/api"
)

func TestPrintUpdate(t *testing.T) {
	old := version
	version = "0.16.0"
	t.Cleanup(func() { version = old })

	var out bytes.Buffer
	printUpdate(&out, api.UpdateStatus{Current: "0.16.0", Enabled: true})
	if out.String() != "" {
		t.Errorf("with nothing new: %q", out.String())
	}

	out.Reset()
	printUpdate(&out, api.UpdateStatus{Current: "0.16.0", Available: &api.UpdateAvailable{Version: "0.17.0", URL: "https://github.com/leciric/agentbox/releases/tag/v0.17.0"}})
	if want := "Update available: AgentBox 0.17.0, https://github.com/leciric/agentbox/releases/tag/v0.17.0\n"; out.String() != want {
		t.Errorf("printUpdate() = %q, want %q", out.String(), want)
	}

	out.Reset()
	printUpdate(&out, api.UpdateStatus{Current: "0.15.0"})
	if want := "The daemon running is 0.15.0: restart it with agentbox daemon stop\n"; out.String() != want {
		t.Errorf("printUpdate() = %q, want %q", out.String(), want)
	}
}
