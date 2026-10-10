package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/hatch"
)

// A call to Hatch's publish_page keeps what the artifacts tray needs of its
// arguments, never the HTML; another tool's arguments aren't kept.
func TestAHatchCallKeepsItsPageArguments(t *testing.T) {
	var u acp.SessionUpdate
	if err := json.Unmarshal([]byte(`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"mcp__hatch__publish_page","status":"pending",
		"rawInput":{"title":"Q3 report","html":"<!doctype html><p>big</p>","expires_in_hours":null},
		"_meta":{"claudeCode":{"toolName":"mcp__hatch__publish_page"}}}`), &u); err != nil {
		t.Fatal(err)
	}
	tool := &api.ChatTool{}
	applyTool(tool, u)
	if want := (&api.ChatPage{Title: "Q3 report", ExpiresInHours: -1}); !reflect.DeepEqual(tool.Page, want) {
		t.Errorf("Page = %+v, want %+v", tool.Page, want)
	}
	// The update that ends it, with no arguments, leaves them.
	applyTool(tool, acp.SessionUpdate{ToolCallID: "t1", Status: "completed"})
	if tool.Page == nil || tool.Page.Title != "Q3 report" {
		t.Errorf("Page after the update = %+v", tool.Page)
	}

	other := &api.ChatTool{}
	title := "Bash"
	applyTool(other, acp.SessionUpdate{ToolCallID: "t2", Title: &title, RawInput: json.RawMessage(`{"command":"ls","title":"x"}`)})
	if other.Page != nil {
		t.Errorf("Bash's Page = %+v", other.Page)
	}
}

// A finished publish_page is in the store by the time it is published: the
// app reads the project's pages again from the store the moment it hears of
// one. The lead's sequence that lost a page: a publish, get_page on it, a
// ToolSearch, then a second permanent page of the same title, finished
// within a second of the store's last write, so stored a second later.
func TestAFinishedPageCallIsStoredBeforeItIsPublished(t *testing.T) {
	store := openStore(t)
	out := func(id string) string {
		return `"Published \"Plan\" (id ` + id + `, version 1): https://hatch.linting.dev/p/` + id + `\n{\"id\":\"` + id + `\",\"version\":1}"`
	}
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"mcp__hatch__publish_page","status":"pending","rawInput":{"title":"Plan","html":"<p>1</p>"},"_meta":{"claudeCode":{"toolName":"mcp__hatch__publish_page"}}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","rawOutput":`+out("dR6ygVLcOlGuJfYu")+`}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t2","title":"mcp__hatch__get_page","status":"pending","rawInput":{"id":"dR6ygVLcOlGuJfYu"},"_meta":{"claudeCode":{"toolName":"mcp__hatch__get_page"}}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t2","status":"completed","rawOutput":"{\"url\":\"https://hatch.linting.dev/p/dR6ygVLcOlGuJfYu\"}"}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t3","title":"ToolSearch","status":"completed","rawInput":{"query":"select:mcp__hatch__update_page,mcp__hatch__set_visibility"},"_meta":{"claudeCode":{"toolName":"ToolSearch"}}}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t4","title":"mcp__hatch__publish_page","status":"pending","rawInput":{"title":"Plan","html":"<p>2</p>","expires_in_hours":null},"_meta":{"claudeCode":{"toolName":"mcp__hatch__publish_page"}}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t4","status":"completed","rawOutput":`+out("sVhkyBNMvUt3KxuV")+`}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	var mu sync.Mutex
	var told, missing []string
	m := &Manager{Store: store, Launch: f.launch, Version: "test", Publish: func(ev api.ChatEvent) {
		it := ev.Item
		if it == nil || it.Tool == nil || it.Tool.Status != "completed" || hatch.Op(it.Tool.Name, it.Tool.Title) == "" {
			return
		}
		stored := false
		rows, err := store.ToolCallsNamed(context.Background(), testAgent.Project, hatch.Publish, hatch.Update)
		for _, row := range rows {
			var got api.ChatItem
			if json.Unmarshal(row.Data, &got) == nil && got.ID == it.ID && got.Tool != nil && got.Tool.Status == "completed" {
				stored = true
			}
		}
		mu.Lock()
		defer mu.Unlock()
		told = append(told, it.Tool.Page.Title)
		if err != nil || !stored {
			missing = append(missing, it.Tool.Output)
		}
	}}
	t.Cleanup(m.Wait)
	t.Cleanup(m.Close)

	if _, err := m.Send(testAgent, "Publish the plan"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 2 {
		t.Errorf("%d finished page calls were published, want 2", len(told))
	}
	if len(missing) > 0 {
		t.Errorf("published before they were stored: %q", missing)
	}
}
