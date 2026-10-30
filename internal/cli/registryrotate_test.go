package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRegistryRotateAsksForAToken is rotate with no flags: an empty token is
// refused before the daemon is asked, and only --no-login means no login.
func TestRegistryRotateAsksForAToken(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(""))
	flags := registryFlags{}
	if _, err := flags.rotateRequest(cmd, "forgejo", false); err == nil {
		t.Fatal("rotate with an empty stdin = nil error, want a refusal")
	}

	cmd.SetIn(strings.NewReader("new-token\n"))
	req, err := (&registryFlags{username: "you", tokenStdin: true}).rotateRequest(cmd, "forgejo", false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Username != "you" || req.Token != "new-token" || req.NoLogin {
		t.Errorf("rotateRequest = %+v, want you/new-token", req)
	}

	req, err = flags.rotateRequest(cmd, "forgejo", true)
	if err != nil || !req.NoLogin || req.Token != "" {
		t.Errorf("rotateRequest(--no-login) = %+v, %v, want NoLogin and no token", req, err)
	}
	if _, err := (&registryFlags{username: "you"}).rotateRequest(cmd, "forgejo", true); err == nil {
		t.Error("--no-login with --username = nil error, want a refusal")
	}
}
