//go:build integration

package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/cursor"
	"agentbox/internal/state"
)

// TestCursorTurn drives AgentBox's own Cursor adapter (internal/cursor) through
// the chat against Cursor itself: a session starts with Cursor's model menu,
// an autonomous agent's session switches to full access by itself, a turn runs
// a shell command and answers, and what it spent lands in the token ledger
// under the model that did the work.
//
// It needs node, a Cursor API key in CURSOR_API_KEY, and the SDK installed
// where AGENTBOX_CURSOR_SDK says (`mise install npm:@cursor/sdk@<pin>`, then
// `mise where` it). It costs one small turn on that account.
func TestCursorTurn(t *testing.T) {
	node, err := exec.LookPath("node")
	sdk := os.Getenv(cursor.SDKEnv)
	if err != nil || sdk == "" || os.Getenv("CURSOR_API_KEY") == "" {
		t.Skip("needs node, CURSOR_API_KEY and " + cursor.SDKEnv)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, cursor.ScriptName)
	if err := os.WriteFile(script, cursor.Script, 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "cursor", Autonomous: true, Worktree: t.TempDir()}
	if err := store.SaveChat(context.Background(), a.Project, a.Name, state.Chat{Options: map[string]string{"model": "claude-haiku-4-5"}}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		Store:   store,
		Version: "test",
		Publish: func(api.ChatEvent) {},
		Launch: func(ctx context.Context, _ state.Agent, _ func(string)) (*Process, error) {
			cmd := exec.CommandContext(ctx, node, script)
			cmd.Dir = a.Worktree
			cmd.Env = append(os.Environ(), "AGENTBOX_BRIEF=/dev/null", "AGENTBOX_CURSOR_MCP="+filepath.Join(dir, "none.json"))
			return StartCommand(cmd)
		},
	}
	t.Cleanup(m.Close)

	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	wait := func(what string, ok func(api.ChatThread) bool) api.ChatThread {
		t.Helper()
		for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			th, err := m.Thread(a)
			if err != nil {
				t.Fatal(err)
			}
			if th.Session.Error != "" {
				t.Fatalf("the session failed: %s", th.Session.Error)
			}
			if ok(th) {
				return th
			}
			if time.Now().After(deadline) {
				var items []string
				for _, it := range th.Items {
					items = append(items, fmt.Sprintf("%s(streaming=%v): %.120q", it.Kind, it.Streaming, it.Text))
				}
				t.Fatalf("%s didn't happen: state %s, items:\n%s", what, th.Session.State, strings.Join(items, "\n"))
			}
		}
	}
	option := func(th api.ChatThread, id string) string {
		for _, o := range th.Session.Options {
			if o.ID == id {
				return o.Value
			}
		}
		return ""
	}
	th := wait("a ready session on claude-haiku-4-5 with full access", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady && option(th, "model") == "claude-haiku-4-5" && option(th, "mode") == "full-access"
	})
	for _, o := range th.Session.Options {
		if o.ID == "model" && len(o.Choices) < 2 {
			t.Errorf("Cursor's model menu: %+v", o.Choices)
		}
	}

	if _, err := m.Send(a, "Run the shell command `echo agentbox-$((40+2))` and reply with exactly what it printed, nothing else."); err != nil {
		t.Fatal(err)
	}
	th = wait("the turn finishing", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady && len(th.Items) > 0 && th.Items[len(th.Items)-1].Kind == "assistant" && !th.Items[len(th.Items)-1].Streaming
	})
	var ranShell bool
	for _, it := range th.Items {
		if it.Kind == "tool" && it.Tool != nil && strings.Contains(it.Tool.Title, "echo agentbox-") {
			ranShell = true
		}
	}
	if !ranShell {
		t.Errorf("no shell tool call in %+v", th.Items)
	}
	if reply := th.Items[len(th.Items)-1].Text; !strings.Contains(reply, "agentbox-42") {
		t.Errorf("the reply = %q", reply)
	}
	var rows []state.TokenRow
	for range 50 {
		if rows, err = store.TokenRows(context.Background(), state.TokenFilter{Project: a.Project, Agent: a.Name}, 10); err == nil && len(rows) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(rows) == 0 || rows[0].AI != "cursor" || rows[0].Model == "" {
		t.Errorf("the token ledger: %+v, %v", rows, err)
	}
}
