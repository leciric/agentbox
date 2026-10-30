package agent_test

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// A Gemini CLI agent needs a login and nothing in the image: every base has
// Gemini CLI. Its model is Gemini CLI's own id, stored for the chat to apply as
// its session starts, and it has no effort. Its chat is Gemini CLI's own ACP
// mode, started without the variables that would replace its login, and with
// an API key login read into GEMINI_API_KEY from the file AgentBox wrote.
func TestGeminiAgent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]' ;;
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

	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "gemini"}); err == nil || !strings.Contains(err.Error(), "agentbox auth gemini") {
		t.Fatalf("Gemini CLI with no login: %v", err)
	}
	if f.m.GeminiReady() {
		t.Error("GeminiReady with no login")
	}
	if err := f.m.Creds.SaveToolAccount("gemini", "", "", []byte(`{"GEMINI_API_KEY":"AIza-test"}`)); err != nil {
		t.Fatal(err)
	}
	if !f.m.GeminiReady() {
		t.Error("GeminiReady with a login")
	}
	if err := f.m.ChatChoices(ctx, "gemini", nil, choice("high")); err == nil || !strings.Contains(err.Error(), "Gemini CLI agents have none") {
		t.Errorf("a Gemini CLI effort: %v", err)
	}
	if err := f.st.SetSetting(ctx, state.SettingDefaultGeminiModel, "gemini-3.8-flash"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "gemini", Autonomous: true})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := f.st.Chat(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Options["model"] != "gemini-3.8-flash" || a.GeminiAccount != "default" || a.Interface != state.InterfaceChat {
		t.Errorf("a new Gemini CLI agent: options %+v, account %q, interface %s", chat.Options, a.GeminiAccount, a.Interface)
	}

	cmd, err := f.m.ChatCommand(ctx, a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	script := cmd.Args[len(cmd.Args)-1]
	for _, want := range []string{"exec env -u GEMINI_API_KEY", `GEMINI_API_KEY="$(cat '/home/`, `/.gemini/agentbox-api-key')" mise exec 'npm:@google/gemini-cli@`, "-- 'gemini' '--acp'"} {
		if !strings.Contains(script, want) {
			t.Errorf("the chat's command %q doesn't have %q", script, want)
		}
	}

	// It has a command line too, unlike Cursor.
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "gemini", Interface: state.InterfaceCLI}); err != nil {
		t.Errorf("a Gemini CLI agent on the command line: %v", err)
	}
}
