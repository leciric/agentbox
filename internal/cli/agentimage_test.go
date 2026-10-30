package cli

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

// TestPushToAPublicRegistryIsRefused keeps the agent image, which carries
// Claude Code and the ACP adapters, out of a registry anyone may pull from,
// whichever stage is built.
func TestPushToAPublicRegistryIsRefused(t *testing.T) {
	public := api.Registry{Name: "dockerhub", URL: "docker.io/you", Public: true}
	for _, stage := range []string{"", "agent", "dev"} {
		err := checkPushTo(public, stage)
		if err == nil {
			t.Fatalf("checkPushTo(public, %q) = nil, want a refusal", stage)
		}
		for _, want := range []string{"dockerhub is public", "Claude Code", "without --public"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("checkPushTo(public, %q) = %q, want it to say %q", stage, err, want)
			}
		}
	}
}

func TestPushToAPrivateRegistryIsAllowed(t *testing.T) {
	private := api.Registry{Name: "forgejo", URL: "hub.example.com/you"}
	for _, stage := range []string{"", "agent", "dev"} {
		if err := checkPushTo(private, stage); err != nil {
			t.Errorf("checkPushTo(private, %q) = %v, want nil", stage, err)
		}
	}
}
