package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentbox/internal/connectors/connectorstest"
)

// `agentbox connector tools` and `connector call`: one short MCP session each
// through the relay, initialized, used once and ended.
func TestToolsAndCallToolFromTheShell(t *testing.T) {
	t.Parallel()
	fake := connectorstest.New()
	defer fake.Close()
	s := newService(t)
	r := relayFor(t, s, connected(t, s, fake))
	ctx := context.Background()

	tools, err := r.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "notion-search" || !strings.Contains(string(tools[0].InputSchema), `"query"`) {
		t.Fatalf("Tools = %+v", tools)
	}

	res, err := r.CallTool(ctx, "notion-search", json.RawMessage(`{"query":"onboarding spec"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Text() != "found: onboarding spec" {
		t.Errorf("CallTool = %+v, text %q", res, res.Text())
	}
	if res, err := r.CallTool(ctx, "notion-search", nil); err != nil || res.Text() != "found: " {
		t.Errorf("CallTool with no arguments = %+v, %v", res, err)
	}

	fake.Lock()
	methods := strings.Join(fake.Methods, " ")
	deleted := fake.Deleted
	fake.Unlock()
	if want := "initialize notifications/initialized tools/list"; !strings.HasPrefix(methods, want) {
		t.Errorf("the server saw %q, want it to start %q", methods, want)
	}
	if deleted != 3 {
		t.Errorf("%d sessions ended, want one a call (3)", deleted)
	}
}

// A connector that can't be reached is an error, not an empty list.
func TestToolsWhenTheConnectorIsntConnected(t *testing.T) {
	t.Parallel()
	fake := connectorstest.New()
	defer fake.Close()
	s := newService(t)
	c := connected(t, s, fake)
	r := relayFor(t, s, c)
	if _, err := s.Disconnect(context.Background(), c.Project, c.Agent, c.Name); err != nil {
		t.Fatal(err)
	}
	if tools, err := r.Tools(context.Background()); err == nil {
		t.Errorf("Tools of a disconnected connector = %+v, want an error", tools)
	}
}

func TestToolResultText(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		res  ToolResult
		want string
	}{
		{"text blocks", ToolResult{Content: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"one"}`),
			json.RawMessage(`{"type":"text","text":"two"}`),
		}}, "one\n\ntwo"},
		{"an image as its JSON", ToolResult{Content: []json.RawMessage{
			json.RawMessage(`{"type":"image","data":"AA=="}`),
		}}, `{"type":"image","data":"AA=="}`},
		{"structured content only", ToolResult{StructuredContent: json.RawMessage(`{"n":1}`)}, `{"n":1}`},
		{"nothing", ToolResult{}, ""},
	} {
		if got := c.res.Text(); got != c.want {
			t.Errorf("%s: Text() = %q, want %q", c.name, got, c.want)
		}
	}
}
