package agent

import (
	"strings"
	"testing"
)

// TestBaseSaveDropsSecrets checks the scrub that keeps a key out of a project
// base. A base is kept until another one is saved, and every future agent of
// the project is copied from it, so a secret left inside would outlive the
// agent it was given to and reach agents nobody gave it to. Agent snapshots
// and forks are the other case, and keep it: they are copies of that agent.
func TestBaseSaveDropsSecrets(t *testing.T) {
	script := scrubScript("dev")
	if !strings.Contains(script, ".config/agentbox/secrets.env") {
		t.Errorf("base save doesn't delete the secrets file:\n%s", script)
	}
	if !strings.Contains(script, ".config/agentbox/env") {
		t.Errorf("base save should still delete the env file with the logins:\n%s", script)
	}
}
