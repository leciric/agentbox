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

// TestCopilotCLITurn runs the chat against GitHub Copilot CLI itself
// (`copilot --acp`, its platform binary), in a home of its own, on the token
// in COPILOT_GITHUB_TOKEN as AgentBox's agents get it. The session starts
// with Copilot's model menu, an autonomous agent's session switches itself to
// autopilot, and the model chosen for it is set (AGENTBOX_COPILOT_MODEL, else
// gpt-5-mini, which costs no premium request). A turn runs, reads and edits a
// file in the worktree, and lands in the token ledger as "copilot" under the
// model. Without a token, Copilot refuses the session, and the chat says so.
//
// It needs Copilot's binary: `copilot` on the PATH, or its path in
// AGENTBOX_COPILOT (npm's loader works too, but leaves the binary behind).
func TestCopilotCLITurn(t *testing.T) {
	copilot := os.Getenv("AGENTBOX_COPILOT")
	if copilot == "" {
		var err error
		if copilot, err = exec.LookPath("copilot"); err != nil {
			t.Skip("needs copilot on the PATH, or AGENTBOX_COPILOT")
		}
	}
	token := os.Getenv("COPILOT_GITHUB_TOKEN")
	model := os.Getenv("AGENTBOX_COPILOT_MODEL")
	if model == "" {
		model = "gpt-5-mini"
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".copilot", "settings.json"), []byte(`{"autoUpdate":false,"banner":"never"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "copilot", Autonomous: true, Worktree: t.TempDir()}
	if err := os.WriteFile(filepath.Join(a.Worktree, "notes.txt"), []byte("colour: red\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveChat(context.Background(), a.Project, a.Name, state.Chat{Options: map[string]string{"model": model}}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		Store: store, Version: "test", Publish: func(api.ChatEvent) {},
		Launch: func(ctx context.Context, _ state.Agent, _ func(string)) (*Process, error) {
			cmd := exec.CommandContext(ctx, copilot, "--acp", "--no-auto-update", "--disable-client-telemetry")
			cmd.Dir = a.Worktree
			for _, kv := range os.Environ() {
				name, _, _ := strings.Cut(kv, "=")
				if name != "COPILOT_GITHUB_TOKEN" && name != "GH_TOKEN" && name != "GITHUB_TOKEN" && name != "HOME" {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			cmd.Env = append(cmd.Env, "HOME="+home, "COPILOT_HOME="+filepath.Join(home, ".copilot"), "COPILOT_AUTO_UPDATE=false")
			if token != "" {
				cmd.Env = append(cmd.Env, "COPILOT_GITHUB_TOKEN="+token)
			}
			return StartCommand(cmd)
		},
	}
	t.Cleanup(m.Close)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	if token == "" {
		th := waitThreadFor(t, m, a, 60*time.Second, "Copilot refusing the session", func(th api.ChatThread) bool {
			return strings.Contains(strings.ToLower(th.Session.Error), "auth")
		})
		t.Logf("without a token: %s", th.Session.Error)
		return
	}
	option := func(th api.ChatThread, category string) api.ChatOption {
		for _, o := range th.Session.Options {
			if o.Category == category {
				return o
			}
		}
		return api.ChatOption{}
	}
	th := waitThreadFor(t, m, a, 120*time.Second, "a ready session on "+model+" in autopilot", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady && option(th, "model").Value == model && strings.HasSuffix(option(th, "mode").Value, "#autopilot")
	})
	if got := option(th, "model").Choices; len(got) < 2 {
		t.Errorf("Copilot's model menu: %+v", got)
	}
	for _, o := range th.Session.Options {
		t.Logf("option %s (%s): %s of %d", o.ID, o.Category, o.Value, len(o.Choices))
	}

	if _, err := m.Send(a, "Read notes.txt, then change the colour in it from red to blue. Then reply with exactly: agentbox-42"); err != nil {
		t.Fatal(err)
	}
	th = waitThreadFor(t, m, a, 240*time.Second, "the turn ending", turnsEnded(1))
	var said []string
	for _, it := range th.Items {
		said = append(said, it.Kind+": "+it.Text)
	}
	if reply := th.Items[len(th.Items)-1].Text; !strings.Contains(reply, "agentbox-42") {
		t.Errorf("the reply = %q\n%s", reply, strings.Join(said, "\n"))
	}
	if b, _ := os.ReadFile(filepath.Join(a.Worktree, "notes.txt")); !strings.Contains(string(b), "blue") {
		t.Errorf("notes.txt = %q, want blue", b)
	}
	var rows []state.TokenRow
	var err error
	for range 50 {
		if rows, err = store.TokenRows(context.Background(), state.TokenFilter{Project: a.Project, Agent: a.Name}, 10); err == nil && len(rows) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(rows) == 0 || rows[0].AI != "copilot" || rows[0].Model == "" || rows[0].Input+rows[0].CacheRead == 0 || rows[0].Output == 0 {
		t.Errorf("the token ledger: %+v, %v", rows, err)
	}
	for _, r := range rows {
		t.Logf("ledger: %s in %d out %d cache read %d write %d cost %.6f context %d", r.Model, r.Input, r.Output, r.CacheRead, r.CacheWrite, r.CostUSD, r.Context)
	}
}
