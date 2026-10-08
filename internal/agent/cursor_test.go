package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// cursorLogin gives AgentBox a Cursor sign-in, in the SDK's own format.
func cursorLogin(t *testing.T, f fixture) {
	t.Helper()
	path := f.m.Creds.CursorAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"backendUrl":"https://api2.cursor.sh","apiKey":"key_test","createdAtMs":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A Cursor agent needs a sign-in and nothing in the image: every base has the
// SDK. It has only the chat, and its model and effort are Cursor's own names,
// stored for the chat to apply as its session starts.
func TestCursorAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}},{"name":"ab-hello-stack-agent-03","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.7"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`))
	ctx := context.Background()
	choice := func(v string) *string { return &v }
	options := func(a state.Agent) map[string]string {
		t.Helper()
		chat, err := f.st.Chat(ctx, a.Project, a.Name)
		if err != nil {
			t.Fatal(err)
		}
		return chat.Options
	}

	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "cursor"}); err == nil || !strings.Contains(err.Error(), "agentbox auth cursor") {
		t.Fatalf("Cursor with no sign-in: %v", err)
	}
	if f.m.CursorReady() {
		t.Error("CursorReady with no sign-in")
	}
	cursorLogin(t, f)
	if !f.m.CursorReady() {
		t.Error("CursorReady with a sign-in")
	}
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "cursor", Interface: state.InterfaceCLI}); err == nil || !strings.Contains(err.Error(), "only in the chat") {
		t.Errorf("a Cursor agent on the command line: %v", err)
	}

	// Settings' model and effort, for an agent given neither.
	if err := f.st.SetSetting(ctx, state.SettingDefaultCursorModel, "claude-opus-5-5"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetSetting(ctx, state.SettingDefaultCursorEffort, "high"); err != nil {
		t.Fatal(err)
	}
	plain, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(plain); got["model"] != "claude-opus-5-5" || got["effort"] != "high" || plain.Interface != state.InterfaceChat {
		t.Errorf("with nothing chosen: %+v, %s", got, plain.Interface)
	}
	// A model chosen for this agent doesn't take Settings' effort, which
	// belongs to Settings' model.
	chosen, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "cursor", Model: choice("composer-2")})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(chosen); got["model"] != "composer-2" || got["effort"] != "" {
		t.Errorf("with a model chosen: %+v", got)
	}
	both, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "cursor", Model: choice("gpt-5.5"), Effort: choice("low")})
	if err != nil {
		t.Fatal(err)
	}
	if got := options(both); got["model"] != "gpt-5.5" || got["effort"] != "low" {
		t.Errorf("with both chosen: %+v", got)
	}

	if err := f.m.ChatChoices(ctx, "cursor", choice(" "), nil); err == nil || !strings.Contains(err.Error(), "an empty model isn't a choice") {
		t.Errorf("an empty Cursor model: %v", err)
	}
	if err := f.m.ChatChoices(ctx, "cursor", nil, choice("max")); err != nil {
		t.Errorf("a Cursor effort: %v", err)
	}
	if _, err := f.m.SetInterface(ctx, plain, state.InterfaceCLI); err == nil {
		t.Error("switched a Cursor agent to the command line")
	}

	// The chat runs AgentBox's own adapter with node, on the pinned SDK.
	cmd, err := f.m.ChatCommand(ctx, plain, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	script := cmd.Args[len(cmd.Args)-1]
	for _, want := range []string{`AGENTBOX_CURSOR_SDK="$(mise where 'npm:@cursor/sdk@`, "exec mise exec 'npm:@cursor/sdk@", "-- 'node' '/home/", "/.local/share/agentbox/agentbox-cursor-acp.mjs'"} {
		if !strings.Contains(script, want) {
			t.Errorf("the chat's command %q doesn't have %q", script, want)
		}
	}
}
