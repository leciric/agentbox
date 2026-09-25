//go:build integration

package chat

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// TestOpenCodeACPHandshake drives the real `opencode acp` through the chat, so
// what AgentBox assumes about OpenCode is checked against OpenCode rather than
// against a fake: that it speaks ACP at all, that starting a session works,
// and that the session advertises the model menu the daemon remembers.
//
// It needs OpenCode on PATH and no login: `session/new` answers without one.
// Behind the integration tag with the Incus tests, because it starts a real
// process that takes seconds and reads the machine's own OpenCode
// configuration (D49).
func TestOpenCodeACPHandshake(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode isn't installed on this machine")
	}
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "opencode", Worktree: t.TempDir()}
	m := &Manager{
		Store:   store,
		Version: "test",
		Publish: func(api.ChatEvent) {},
		Launch: func(ctx context.Context, _ state.Agent, _ func(string)) (*Process, error) {
			cmd := exec.CommandContext(ctx, "opencode", "acp")
			cmd.Dir = a.Worktree
			return StartCommand(cmd)
		},
	}
	t.Cleanup(m.Close)

	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	var session api.ChatSession
	for {
		th, err := m.Thread(a)
		if err != nil {
			t.Fatal(err)
		}
		session = th.Session
		if session.State == api.ChatReady || session.Error != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("opencode acp didn't reach a ready session: %+v", session)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if session.Error != "" {
		t.Fatalf("the session failed: %s", session.Error)
	}

	// The model menu is what everything else in this feature stands on: the
	// composer's picker, the lead's choice, and what set_config_option
	// validates a chosen model against.
	var models *api.ChatOption
	for i, o := range session.Options {
		if o.Category == "model" && o.Type == "select" {
			models = &session.Options[i]
		}
	}
	if models == nil {
		t.Fatalf("opencode's session advertises no model menu: %+v", session.Options)
	}
	if len(models.Choices) == 0 {
		t.Error("the model menu is empty")
	}
	for _, choice := range models.Choices {
		if !isProviderModel(choice.Value) {
			t.Errorf("model %q isn't a provider/model id", choice.Value)
		}
	}

	// And the daemon remembers it, under OpenCode's own key.
	var remembered string
	for range 50 {
		remembered, _ = store.Setting(context.Background(), state.SettingOpenCodeModelChoices)
		if remembered != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(state.ChoiceValues(remembered)) == 0 {
		t.Errorf("OpenCode's menu wasn't remembered: %q", remembered)
	}
	if claude, _ := store.Setting(context.Background(), state.SettingClaudeModelChoices); claude != "" {
		t.Errorf("an OpenCode session wrote Claude Code's menu: %q", claude)
	}
}

func isProviderModel(id string) bool {
	provider, model, ok := strings.Cut(id, "/")
	return ok && provider != "" && model != ""
}
