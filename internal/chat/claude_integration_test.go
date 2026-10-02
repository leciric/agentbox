//go:build integration

package chat

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// TestClaudeTurnWithToolsSubagentsAndASteer drives the real claude-agent-acp
// through the chat, the way an agent's chat does, through one turn that uses
// what AgentBox reads of the adapter's tool calls: a shell command and its
// output, a file written and edited and their diffs, the permission each
// asks for, a file read, a subagent
// in the foreground and one in the background, and a message given to the
// turn while the background one still runs. It is what checks AgentBox
// against each new adapter release rather than against a fake: the turn has
// to end, on its own, completed, with every tool call accounted for.
//
// It runs the model, so it needs Claude Code logged in and spends a little of
// the account's usage, on Haiku: it only runs when AGENTBOX_CLAUDE_LIVE is
// set. AGENTBOX_CLAUDE_ACP picks the adapter to run, claude-agent-acp on PATH
// otherwise.
func TestClaudeTurnWithToolsSubagentsAndASteer(t *testing.T) {
	if os.Getenv("AGENTBOX_CLAUDE_LIVE") == "" {
		t.Skip("set AGENTBOX_CLAUDE_LIVE to run Claude Code for real")
	}
	adapter := os.Getenv("AGENTBOX_CLAUDE_ACP")
	if adapter == "" {
		adapter = "claude-agent-acp"
	}
	if _, err := exec.LookPath(adapter); err != nil {
		t.Skip("claude-agent-acp isn't installed on this machine")
	}
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "claude", Worktree: t.TempDir()}
	m := &Manager{
		Store:   store,
		Version: "test",
		Publish: func(api.ChatEvent) {},
		Logf:    t.Logf,
		Launch: func(ctx context.Context, _ state.Agent, _ func(string)) (*Process, error) {
			cmd := exec.CommandContext(ctx, adapter)
			cmd.Dir = a.Worktree
			return StartCommand(cmd)
		},
	}
	t.Cleanup(m.Wait)
	t.Cleanup(m.Close)

	wait := func(what string, within time.Duration, cond func(api.ChatThread) bool) api.ChatThread {
		t.Helper()
		deadline := time.Now().Add(within)
		for {
			th, err := m.Thread(a)
			if err != nil {
				t.Fatal(err)
			}
			if cond(th) {
				return th
			}
			// Somebody at the chat says yes to whatever it asks.
			for _, it := range th.Items {
				if it.Kind != "permission" || it.Permission.Outcome != "" {
					continue
				}
				for _, o := range it.Permission.Options {
					if strings.HasPrefix(o.Kind, "allow") {
						if _, err := m.Answer(a, it.ID, o.ID); err != nil {
							t.Logf("answering %q: %v", it.Permission.Title, err)
						}
						break
					}
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s within %s: %s", what, within, summary(th))
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	th := wait("ready session", 90*time.Second, func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady || th.Session.Error != ""
	})
	if th.Session.Error != "" {
		t.Fatalf("the session failed: %s", th.Session.Error)
	}
	for _, o := range th.Session.Options {
		if o.Category != "model" {
			continue
		}
		for _, c := range o.Choices {
			if strings.Contains(strings.ToLower(c.Value), "haiku") {
				if _, err := m.SetOption(context.Background(), a, o.ID, c.Value); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	if _, err := m.Send(a, "Do these steps in order, one tool call each:\n"+
		"1. Use the Agent tool with run_in_background true, subagent_type general-purpose and model haiku. Its task: run exactly `python3 -c \"import time; time.sleep(45)\" && echo slept` with Bash, then reply DONE. Don't wait for it.\n"+
		"2. Run `echo hello-from-bash` with Bash.\n"+
		"3. Create notes.txt containing the line `one`, with Write.\n"+
		"4. Change `one` to `two` in notes.txt, with Edit.\n"+
		"5. Read notes.txt with Read.\n"+
		"6. Use the Agent tool in the foreground (subagent_type general-purpose, model haiku) to run `cat notes.txt` and report what it says.\n"+
		"Then, once the background subagent reports back, reply with one line: ALL DONE."); err != nil {
		t.Fatal(err)
	}
	// The steer goes in while the background subagent still runs.
	wait("background subagent", 2*time.Minute, func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "subagent" && it.Subagent.State == subagentRunning {
				return true
			}
		}
		return false
	})
	time.Sleep(10 * time.Second)
	if _, err := m.Send(a, "Aside, while that runs: what is 2+2? Answer in one line, then carry on."); err != nil {
		t.Fatal(err)
	}

	th = wait("end to the turn", 5*time.Minute, turnsEnded(1))
	var users, asides, cards int
	tools := map[string][]api.ChatTool{}
	for _, it := range th.Items {
		switch it.Kind {
		case "user":
			users++
			if it.Result != nil && it.Result.State != "completed" {
				t.Errorf("the turn ended %s (%s): %s", it.Result.State, it.Result.StopReason, summary(th))
			}
		case "aside":
			asides++
			if it.Delivery != api.ChatAsideSent {
				t.Errorf("the steer was %s, want sent into the running turn", it.Delivery)
			}
		case "error":
			t.Errorf("an error in the conversation: %s", it.Text)
		case "subagent":
			cards++
			if it.Subagent.State != "completed" {
				t.Errorf("subagent %q ended %s", it.Subagent.Name, it.Subagent.State)
			}
		case "tool":
			if it.Parent == "" {
				tools[it.Tool.Name] = append(tools[it.Tool.Name], *it.Tool)
			}
			// A subagent's tool calls may fail on their own; the agent's own
			// must have ended, and none may be left running.
			switch it.Tool.Status {
			case "pending", "in_progress", "stopped":
				t.Errorf("%s call %q (%s) is %s after the turn", it.Tool.Name, it.Tool.Title, it.Tool.CallID, it.Tool.Status)
			}
		}
	}
	if users != 1 || asides != 1 || cards < 2 {
		t.Errorf("%d user messages, %d asides and %d subagent cards, want the prompt, the steer and two subagents: %s", users, asides, cards, summary(th))
	}
	ok := func(name string, check func(api.ChatTool) bool) {
		t.Helper()
		for _, tool := range tools[name] {
			if check(tool) {
				return
			}
		}
		t.Errorf("no %s call as expected: %+v", name, tools[name])
	}
	ok("Bash", func(c api.ChatTool) bool {
		return c.Status == "completed" && strings.Contains(c.Command, "hello-from-bash") && strings.Contains(c.Output, "hello-from-bash")
	})
	ok("Write", func(c api.ChatTool) bool {
		return c.Status == "completed" && len(c.Diffs) == 1 && strings.Contains(c.Diffs[0].NewText, "one")
	})
	ok("Edit", func(c api.ChatTool) bool {
		return c.Status == "completed" && len(c.Diffs) == 1 && strings.Contains(c.Diffs[0].OldText, "one") && strings.Contains(c.Diffs[0].NewText, "two")
	})
	ok("Read", func(c api.ChatTool) bool { return c.Status == "completed" && len(c.Paths) == 1 })
	ok("Agent", func(c api.ChatTool) bool { return c.Status == "completed" })
	if last := m.LastMessage(a); !strings.Contains(last, "ALL DONE") {
		t.Errorf("the turn's last word is %q, want ALL DONE", last)
	}
}
