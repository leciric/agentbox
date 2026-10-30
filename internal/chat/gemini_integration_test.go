//go:build integration

package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// TestGeminiCLITurn runs the chat against Gemini CLI itself (`gemini --acp`),
// in a home of its own: the session starts with Gemini CLI's model menu, an
// autonomous agent's session switches itself to YOLO, and the model chosen
// for it is set. With a real key in GEMINI_API_KEY a turn runs and lands in
// the token ledger as "gemini" under the model; without one the key is a
// placeholder, which Gemini CLI takes until it asks Google, so the turn fails
// with Google's refusal and the chat says so.
//
// It needs Gemini CLI: `gemini` on the PATH, or its path in AGENTBOX_GEMINI.
func TestGeminiCLITurn(t *testing.T) {
	gemini := os.Getenv("AGENTBOX_GEMINI")
	if gemini == "" {
		var err error
		if gemini, err = exec.LookPath("gemini"); err != nil {
			t.Skip("needs gemini on the PATH, or AGENTBOX_GEMINI")
		}
	}
	key, real := os.Getenv("GEMINI_API_KEY"), true
	if key == "" {
		key, real = "not-a-real-key", false
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"security":{"auth":{"selectedType":"gemini-api-key"},"folderTrust":{"enabled":false}},"general":{"enableAutoUpdate":false}}`
	if err := os.WriteFile(filepath.Join(home, ".gemini", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "gemini", Autonomous: true, Worktree: t.TempDir()}
	if err := store.SaveChat(context.Background(), a.Project, a.Name, state.Chat{Options: map[string]string{"model": "gemini-3.8-flash"}}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		Store: store, Version: "test", Publish: func(api.ChatEvent) {},
		Launch: func(ctx context.Context, _ state.Agent, _ func(string)) (*Process, error) {
			cmd := exec.CommandContext(ctx, gemini, "--acp")
			cmd.Dir = a.Worktree
			cmd.Env = append(os.Environ(), "HOME="+home, "GEMINI_API_KEY="+key)
			return StartCommand(cmd)
		},
	}
	t.Cleanup(m.Close)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	option := func(th api.ChatThread, id string) api.ChatOption {
		for _, o := range th.Session.Options {
			if o.ID == id {
				return o
			}
		}
		return api.ChatOption{}
	}
	th := waitThreadFor(t, m, a, 90*time.Second, "a ready session on gemini-3.8-flash in YOLO", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady && option(th, "model").Value == "gemini-3.8-flash" && option(th, "mode").Value == "yolo"
	})
	if got := option(th, "model").Choices; len(got) < 2 {
		t.Errorf("Gemini CLI's model menu: %+v", got)
	}
	t.Logf("adapter %s, models %d, modes %d", th.Session.Adapter, len(option(th, "model").Choices), len(option(th, "mode").Choices))

	if _, err := m.Send(a, "Reply with exactly: agentbox-42"); err != nil {
		t.Fatal(err)
	}
	th = waitThreadFor(t, m, a, 120*time.Second, "the turn ending", turnsEnded(1))
	var said []string
	for _, it := range th.Items {
		said = append(said, it.Kind+": "+it.Text)
	}
	if !real {
		if !strings.Contains(strings.ToLower(strings.Join(said, "\n")), "api key") {
			t.Errorf("the chat doesn't say Google refused the key:\n%s", strings.Join(said, "\n"))
		}
		return
	}
	if reply := th.Items[len(th.Items)-1].Text; !strings.Contains(reply, "agentbox-42") {
		t.Errorf("the reply = %q", reply)
	}
	var rows []state.TokenRow
	var err error
	for range 50 {
		if rows, err = store.TokenRows(context.Background(), state.TokenFilter{Project: a.Project, Agent: a.Name}, 10); err == nil && len(rows) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(rows) == 0 || rows[0].AI != "gemini" || rows[0].Model == "" {
		t.Errorf("the token ledger: %+v, %v", rows, err)
	}
}

// waitThreadFor is waitThread with a deadline of its own, for a real tool.
func waitThreadFor(t *testing.T, m *Manager, a state.Agent, d time.Duration, what string, cond func(api.ChatThread) bool) api.ChatThread {
	t.Helper()
	for deadline := time.Now().Add(d); ; time.Sleep(200 * time.Millisecond) {
		th, err := m.Thread(a)
		if err != nil {
			t.Fatal(err)
		}
		if cond(th) {
			return th
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s didn't happen: state %s, error %q, %d items", what, th.Session.State, th.Session.Error, len(th.Items))
		}
	}
}
