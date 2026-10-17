package chat

import (
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

func TestWhichToolCallsUseTheDesktop(t *testing.T) {
	for _, c := range []struct {
		tool api.ChatTool
		want bool
	}{
		{api.ChatTool{Name: "mcp__desktop__click", Title: "mcp__desktop__click"}, true},
		{api.ChatTool{Name: "mcp__playwright__browser_navigate"}, true},
		{api.ChatTool{Title: "desktop/screenshot"}, true},
		{api.ChatTool{Title: "playwright.browser_click"}, true},
		{api.ChatTool{Name: "Bash", Command: "agentbox media record start --input desktop --name demo"}, true},
		{api.ChatTool{Name: "Bash", Command: "agentbox browser open http://localhost:5173"}, true},
		{api.ChatTool{Name: "Bash", Command: "agentbox media screenshot --name x"}, true},
		{api.ChatTool{Name: "Bash", Command: "agentbox media note \"checks the export\""}, false},
		{api.ChatTool{Name: "Bash", Command: "go test ./..."}, false},
		{api.ChatTool{Name: "mcp__hatch__publish_page"}, false},
		{api.ChatTool{Name: "Edit", Title: "Edit desktop/src/App.tsx"}, false},
		{api.ChatTool{Title: "Read desktop.go"}, false},
	} {
		if got := usesDesktop(&c.tool); got != c.want {
			t.Errorf("usesDesktop(%+v) = %v, want %v", c.tool, got, c.want)
		}
	}
}

// A turn that drives the desktop, itself or through a subagent, stamps when
// it did; one that doesn't leaves no stamp.
func TestAnAgentUsingItsDesktopIsSeen(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		if text == "Check the page" {
			f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"mcp__desktop__click","status":"pending","_meta":{"claudeCode":{"toolName":"mcp__desktop__click"}}}`)
			f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}`)
		} else {
			f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t2","title":"Bash","status":"completed","rawInput":{"command":"go test ./..."},"_meta":{"claudeCode":{"toolName":"Bash"}}}`)
		}
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m := &Manager{Store: store, Launch: f.launch, Version: "test"}
	t.Cleanup(m.Wait)
	t.Cleanup(m.Close)

	if _, err := m.Send(testAgent, "Run the tests"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if at := m.DesktopAt(testAgent.Ref()); !at.IsZero() {
		t.Fatalf("DesktopAt after a turn without the desktop = %v, want zero", at)
	}
	if _, err := m.Send(testAgent, "Check the page"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(2))
	if m.DesktopAt(testAgent.Ref()).IsZero() {
		t.Fatal("DesktopAt after a desktop click is zero")
	}
	if !m.DesktopAt("other/agent-1").IsZero() {
		t.Error("an agent with no chat has a DesktopAt")
	}
}
