package cli_test

import (
	"strings"
	"testing"

	"agentbox/internal/testutil"
)

func TestIOSCmdsOnAnUnknownAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"ios", "list", "hello-stack/agent-01"},
		{"ios", "open", "hello-stack/agent-01"},
		{"ios", "tap", "hello-stack/agent-01", "10", "20"},
		{"ios", "swipe", "hello-stack/agent-01", "1", "2", "3", "4"},
		{"ios", "type", "hello-stack/agent-01", "hello"},
		{"ios", "button", "hello-stack/agent-01", "home"},
		{"ios", "install", "hello-stack/agent-01", "Pawly.app"},
		{"ios", "launch", "hello-stack/agent-01", "com.example.pawly"},
		{"ios", "screenshot", "hello-stack/agent-01"},
	} {
		if _, err := run(t, "", args...); err == nil {
			t.Errorf("agentbox %s on an unknown agent succeeded", strings.Join(args, " "))
		}
	}
	if _, err := run(t, "", "ios", "tap", "hello-stack/agent-01", "ten", "20"); err == nil || !strings.Contains(err.Error(), "isn't a number") {
		t.Errorf("tap with a word for x: %v", err)
	}
}
