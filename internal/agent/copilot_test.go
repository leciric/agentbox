package agent_test

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// A GitHub Copilot CLI agent needs a login: a GitHub token, stored as an
// account. Its model and effort are Copilot's own ids, stored for the chat to
// apply as its session starts. Its chat is Copilot's own ACP mode, run as the
// platform binary rather than npm's loader, started without the variables
// that would replace its login and with the account's token read from a file
// into COPILOT_GITHUB_TOKEN, never written into the command.
func TestCopilotAgent(t *testing.T) {
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

	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "copilot"}); err == nil || !strings.Contains(err.Error(), "agentbox auth copilot") {
		t.Fatalf("Copilot with no login: %v", err)
	}
	if f.m.CopilotReady() {
		t.Error("CopilotReady with no login")
	}
	if err := f.m.Creds.SaveToolAccount("copilot", "", "", []byte(`{"COPILOT_GITHUB_TOKEN":"github_pat_test","login":"octocat"}`)); err != nil {
		t.Fatal(err)
	}
	if !f.m.CopilotReady() {
		t.Error("CopilotReady with a login")
	}
	if err := f.m.ChatChoices(ctx, "copilot", choice(""), nil); err == nil {
		t.Error("an empty Copilot model was taken")
	}
	if err := f.st.SetSetting(ctx, state.SettingDefaultCopilotModel, "claude-sonnet-5.5"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "copilot", Autonomous: true, Effort: choice("high")})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := f.st.Chat(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Options["model"] != "claude-sonnet-5.5" || chat.Options["effort"] != "high" || a.CopilotAccount != "default" || a.Interface != state.InterfaceChat {
		t.Errorf("a new Copilot agent: options %+v, account %q, interface %s", chat.Options, a.CopilotAccount, a.Interface)
	}

	cmd, err := f.m.ChatCommand(ctx, a, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	script := cmd.Args[len(cmd.Args)-1]
	for _, want := range []string{
		"COPILOT_AUTO_UPDATE=false exec env -u COPILOT_GITHUB_TOKEN",
		`COPILOT_GITHUB_TOKEN="$(cat '/home/`,
		"/.copilot/agentbox-token')",
		"mise exec 'npm:@github/copilot@",
		`-path "*/@github/copilot-linux-`,
		"'--acp' '--no-auto-update' '--disable-client-telemetry'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the chat's command %q doesn't have %q", script, want)
		}
	}
	if strings.Contains(script, "github_pat_test") {
		t.Errorf("the chat's command holds the token: %q", script)
	}
	if strings.Contains(script, "-- 'copilot'") {
		t.Errorf("the chat runs npm's loader, which would leave Copilot running when stopped: %q", script)
	}

	// It has a command line too.
	if _, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "copilot", Interface: state.InterfaceCLI}); err != nil {
		t.Errorf("a Copilot agent on the command line: %v", err)
	}
}
