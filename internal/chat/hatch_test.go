package chat

import (
	"encoding/json"
	"reflect"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
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
