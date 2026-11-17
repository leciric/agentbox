package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// localOutput reads a task's output file from this machine, the way the
// daemon reads it from the agent's.
func localOutput(_ context.Context, _ state.Agent, path string, before, limit int64) ([]byte, int64, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	size := int64(len(data))
	end := size
	if before > 0 && before < size {
		end = before
	}
	start := max(end-limit, 0)
	return data[start:end], start, size, nil
}

func taskOf(th api.ChatThread, id string) *api.ChatBackgroundTask {
	for i := range th.Session.BackgroundTasks {
		if th.Session.BackgroundTasks[i].ID == id {
			return &th.Session.BackgroundTasks[i]
		}
	}
	return nil
}

// A background command is listed with what the adapter said of it and the
// command of the tool call that started it, its output is read from its
// file a piece at a time, and once it ends it stays listed with how it
// ended and what its output was, even after the file is gone.
func TestABackgroundCommandIsListedWithItsStatusAndOutput(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "b1.output")
	if err := os.WriteFile(out, []byte("line 1\nline 2\nline 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"bash-1","title":"gh run watch 42","kind":"execute","status":"in_progress","rawInput":{"command":"gh run watch 42 --exit-status"}}`)
		f.update(s, `{"sessionUpdate":"async_task_spawned","asyncTaskId":"b1","name":"Bash","taskType":"shell","description":"Watch CI run 42","toolCallId":"bash-1","outputFilePath":"`+out+`"}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"bash-1","status":"completed"}`)
		f.update(s, `{"sessionUpdate":"async_task_spawned","asyncTaskId":"m1","name":"Monitor","taskType":"monitor","description":"Deploy log errors"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	m.ReadOutput = localOutput
	if _, err := m.Send(testAgent, "watch CI"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	b1, m1 := taskOf(th, "b1"), taskOf(th, "m1")
	if b1 == nil || m1 == nil || len(th.Session.BackgroundTasks) != 2 {
		t.Fatalf("the session's tasks = %+v, want the command and the monitor", th.Session.BackgroundTasks)
	}
	if b1.Kind != api.BackgroundCommand || b1.Title != "Watch CI run 42" || b1.Status != api.BackgroundRunning || b1.StartedAt.IsZero() || b1.EndedAt != nil || !b1.HasOutput || b1.Item == "" {
		t.Errorf("the command = %+v", *b1)
	}
	if tool := find(th, "tool", 0); b1.Item != tool.ID || b1.Command != tool.Tool.Command {
		t.Errorf("the command's item = %q and command = %q, want its tool call %q (%q)", b1.Item, b1.Command, tool.ID, tool.Tool.Command)
	}
	if m1.Kind != api.BackgroundMonitor || m1.HasOutput || m1.Status != api.BackgroundRunning {
		t.Errorf("the monitor = %+v", *m1)
	}

	// The end of its output, then the piece before it.
	d, err := m.BackgroundTask(context.Background(), testAgent, "b1", 0, 7)
	if err != nil || d.Output != "line 3\n" || d.OutputStart != 14 || d.OutputSize != 21 {
		t.Fatalf("the output's end = %+v, %v", d, err)
	}
	d, err = m.BackgroundTask(context.Background(), testAgent, "b1", d.OutputStart, 100)
	if err != nil || d.Output != "line 1\nline 2\n" || d.OutputStart != 0 {
		t.Fatalf("the piece before = %+v, %v", d, err)
	}
	if _, err := m.BackgroundTask(context.Background(), testAgent, "nope", 0, 0); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("an unknown task's error = %v, want not found", err)
	}

	// It fails: listed still, with its exit code, and its output outlives
	// the file.
	// As claude-agent-acp 0.104 says it: the state alone, then how it ended
	// as the description of the turn it woke, which is no background work.
	f.update("session-1", `{"sessionUpdate":"async_task_state_update","asyncTaskId":"b1","state":"failed"}`)
	f.update("session-1", `{"sessionUpdate":"async_task_spawned","asyncTaskId":"autonomous-turn-1","name":"Claude is working","taskType":"turn","description":"Background command \"Watch CI run 42\" failed with exit code 2"}`)
	f.update("session-1", `{"sessionUpdate":"async_task_state_update","asyncTaskId":"m1","state":"failed","summary":"Monitor timed out after 30m"}`)
	waitFor(t, "the ended command's output to be kept", func() bool {
		c := m.existing(testAgent.Ref())
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.adapter != nil && c.adapter.tasks["b1"].tail != ""
	})
	th = waitThread(t, m, testAgent, "the tasks to end", func(th api.ChatThread) bool {
		b, mo := taskOf(th, "b1"), taskOf(th, "m1")
		return b != nil && mo != nil && b.ExitCode != nil && mo.EndedAt != nil
	})
	b1, m1 = taskOf(th, "b1"), taskOf(th, "m1")
	if taskOf(th, "autonomous-turn-1") != nil {
		t.Errorf("the woken turn is listed as background work: %+v", th.Session.BackgroundTasks)
	}
	if b1.Status != api.BackgroundFailed || b1.ExitCode == nil || *b1.ExitCode != 2 || b1.Summary == "" {
		t.Errorf("the failed command = %+v", *b1)
	}
	if m1.Status != api.BackgroundTimedOut || m1.ExitCode != nil {
		t.Errorf("the timed-out monitor = %+v", *m1)
	}
	if len(th.Session.Background) != 0 {
		t.Errorf("ended work still counts as running: %q", th.Session.Background)
	}
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	d, err = m.BackgroundTask(context.Background(), testAgent, "b1", 0, 0)
	if err != nil || d.Output != "line 1\nline 2\nline 3\n" || d.OutputError != "" {
		t.Errorf("the output after its file went = %+v, %v", d, err)
	}
}

// codex-acp names no output file: the task shows what its tool call kept.
func TestABackgroundTaskWithNoFileShowsItsToolCallsOutput(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"term-1","title":"npm run dev","kind":"execute","status":"in_progress","rawInput":{"command":"npm run dev"}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"term-1","content":[{"type":"content","content":{"type":"text","text":"listening on :3000"}}]}`)
		f.update(s, `{"sessionUpdate":"async_task_spawned","asyncTaskId":"t1","name":"npm run dev","taskType":"shell","toolCallId":"term-1"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "start the server"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	task := taskOf(th, "t1")
	if task == nil || task.Title != "npm run dev" || !task.HasOutput {
		t.Fatalf("the task = %+v", th.Session.BackgroundTasks)
	}
	d, err := m.BackgroundTask(context.Background(), testAgent, "t1", 0, 0)
	if err != nil || d.Output != "listening on :3000" {
		t.Errorf("its output = %+v, %v", d, err)
	}
}

// A subagent still running when its turn ends is background work that
// points at its card; one that ended within its turn is not.
func TestASubagentThatOutlivesItsTurnIsBackgroundWork(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"fg-1","name":"Explore","task":"find it"}`)
		f.update(s, `{"sessionUpdate":"subagent_state_update","subagentSessionId":"fg-1","state":"completed"}`)
		f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"bg-1","name":"general-purpose","task":"run the suite"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", func(th api.ChatThread) bool {
		return turnsEnded(1)(th) && len(th.Session.BackgroundTasks) > 0
	})
	if len(th.Session.BackgroundTasks) != 1 {
		t.Fatalf("tasks = %+v, want only the subagent still running", th.Session.BackgroundTasks)
	}
	task := th.Session.BackgroundTasks[0]
	card := find(th, "subagent", 1)
	if task.Kind != api.BackgroundSubagent || task.Title != "general-purpose" || task.Command != "run the suite" || task.Item != card.ID || task.Status != api.BackgroundRunning || task.HasOutput {
		t.Errorf("the subagent's task = %+v (card %s)", task, card.ID)
	}
	f.update("session-1", `{"sessionUpdate":"subagent_state_update","subagentSessionId":"bg-1","state":"failed"}`)
	th = waitThread(t, m, testAgent, "the subagent to end", func(th api.ChatThread) bool {
		return len(th.Session.BackgroundTasks) == 1 && th.Session.BackgroundTasks[0].EndedAt != nil
	})
	if got := th.Session.BackgroundTasks[0]; got.Status != api.BackgroundFailed {
		t.Errorf("the ended subagent's task = %+v", got)
	}
	if d, err := m.BackgroundTask(context.Background(), testAgent, task.ID, 0, 0); err != nil || d.Task.Item != card.ID {
		t.Errorf("its detail = %+v, %v", d, err)
	}
}

func TestWholeRunesCutsPiecesAtCharacterBoundaries(t *testing.T) {
	t.Parallel()
	all := []byte("aé€b")                  // 1 + 2 + 3 + 1 bytes
	data, start := wholeRunes(all[2:5], 2) // the é's second byte, and two of the €'s three
	if string(data) != "" || start != 3 {
		t.Errorf("a piece of only halves = %q at %d", data, start)
	}
	data, start = wholeRunes(all[2:], 2)
	if string(data) != "€b" || start != 3 {
		t.Errorf("a piece starting mid-character = %q at %d", data, start)
	}
	data, start = wholeRunes(all[:2], 0)
	if string(data) != "a" || start != 0 {
		t.Errorf("a piece ending mid-character = %q at %d", data, start)
	}
}
