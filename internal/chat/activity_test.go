package chat

import (
	"slices"
	"testing"

	"agentbox/internal/acp"
)

// TestOpenCodeTwoToolUpdatesKeepTheirTextAndNameTheFile uses payloads captured
// from OpenCode 2: the human text is in content blocks while rawOutput is only
// metadata, and read/edit rows are titled with the bare tool name.
func TestOpenCodeTwoToolUpdatesKeepTheirTextAndNameTheFile(t *testing.T) {
	t.Parallel()
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"read","title":"read","name":"read","kind":"read","status":"in_progress","locations":[{"path":"/tmp/oc2/foo.txt"}]}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"read","status":"completed","locations":[{"path":"/tmp/oc2/foo.txt"}],"content":[{"type":"content","content":{"type":"text","text":"Read file foo.txt, lines 1-2\n1: hello world\n2: second line"}}],"rawOutput":{"metadata":{"truncated":false}}}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"shell","title":"bash","name":"bash","kind":"execute","status":"in_progress","rawInput":{"command":["bash","-lc","printf hi"]}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"shell","status":"completed","content":[{"type":"content","content":{"type":"text","text":"hi\n"}}],"rawOutput":{"metadata":{"truncated":false}}}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"write","title":"write","name":"write","kind":"edit","status":"completed","locations":[{"path":"/tmp/oc2/new.txt"}],"rawInput":{"filePath":"/tmp/oc2/new.txt","content":"hello\n"}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "look around"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))

	read := find(th, "tool", 0).Tool
	if read.Title != "foo.txt" {
		t.Errorf("read title = %q, want the file's name", read.Title)
	}
	if want := "Read file foo.txt, lines 1-2\n1: hello world\n2: second line"; read.Output != want {
		t.Errorf("read output = %q, want %q", read.Output, want)
	}
	shell := find(th, "tool", 1).Tool
	if shell.Output != "hi\n" {
		t.Errorf("shell output = %q, want its content text", shell.Output)
	}
	write := find(th, "tool", 2).Tool
	if write.Title != "new.txt" {
		t.Errorf("write title = %q, want the file's name", write.Title)
	}
	if len(write.Diffs) != 1 || !write.Diffs[0].Created || write.Diffs[0].Path != "/tmp/oc2/new.txt" || write.Diffs[0].NewText != "hello\n" {
		t.Errorf("write's diffs = %+v, want the created file", write.Diffs)
	}
}

func TestRawTextIgnoresMetadataOnlyObjects(t *testing.T) {
	t.Parallel()
	if got := rawText([]byte(`{"metadata":{"truncated":false}}`)); got != "" {
		t.Errorf("rawText(metadata) = %q, want no text", got)
	}
	if got := rawText([]byte(`{"output":"readable"}`)); got != "readable" {
		t.Errorf("rawText(output) = %q, want readable", got)
	}
}

// TestTodoWriteBecomesThePlan: OpenCode reports todos as a tool call, and the
// chat shows them as its plan instead of a row per update.
func TestTodoWriteBecomesThePlan(t *testing.T) {
	t.Parallel()
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"todo-1","title":"todowrite","name":"todowrite","kind":"other","status":"in_progress","rawInput":{"todos":[{"content":"list files","status":"in_progress","priority":"high"},{"content":"read foo.txt","status":"pending","priority":"medium"}]}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"todo-1","status":"completed"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "make a plan"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if got := kinds(th); !slices.Equal(got, []string{"user", "plan"}) {
		t.Fatalf("items %v, want the plan and no tool row", got)
	}
	plan := find(th, "plan", 0).Plan
	want := []string{"list files", "read foo.txt"}
	got := []string{}
	for _, e := range plan {
		got = append(got, e.Content)
	}
	if !slices.Equal(got, want) || plan[0].Status != "in_progress" || plan[1].Status != "pending" {
		t.Errorf("plan = %+v, want %v", plan, want)
	}
}

// TestPlanUpdateAndRemoved: a v2 client replaces a plan with items or Markdown
// and can remove it.
func TestPlanUpdateAndRemoved(t *testing.T) {
	t.Parallel()
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"plan_update","plan":{"type":"markdown","planId":"plan-mode","content":"## Plan\n- [x] Read foo.txt\n- [ ] Write bar.txt"}}`)
		f.update(s, `{"sessionUpdate":"plan_update","plan":{"type":"items","planId":"plan","entries":[{"content":"one","priority":"high","status":"in_progress"}]}}`)
		f.update(s, `{"sessionUpdate":"plan_removed","planId":"plan"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "plan it"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if plan := find(th, "plan", 0).Plan; len(plan) != 0 {
		t.Errorf("plan = %+v after plan_removed, want it gone", plan)
	}
}

func TestPlanUpdateMarkdown(t *testing.T) {
	t.Parallel()
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"plan_update","plan":{"type":"markdown","planId":"plan-mode","content":"## Plan\n- [x] Read foo.txt\n- [ ] Write bar.txt"}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "plan it"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	plan := find(th, "plan", 0).Plan
	if len(plan) != 2 || plan[0].Content != "Read foo.txt" || plan[0].Status != "completed" || plan[1].Content != "Write bar.txt" || plan[1].Status != "pending" {
		t.Errorf("plan = %+v", plan)
	}
}
