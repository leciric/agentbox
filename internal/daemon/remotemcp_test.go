package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/hubtest"
)

// mcpCall sends one MCP request to the hub's endpoint as an app would, and
// answers the HTTP status and the JSON-RPC result.
func mcpCall(t *testing.T, hub, token, method string, params any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, hub+hubapi.PathMCP, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var msg struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	_ = json.Unmarshal(data, &msg)
	if msg.Error != nil {
		return resp.StatusCode, map[string]any{"rpcError": msg.Error["message"]}
	}
	return resp.StatusCode, msg.Result
}

// mcpTool calls a tool and answers its text, and whether it failed.
func mcpTool(t *testing.T, hub, token, name string, args map[string]any) (string, bool) {
	t.Helper()
	status, res := mcpCall(t, hub, token, "tools/call", map[string]any{"name": name, "arguments": args})
	if status != http.StatusOK {
		t.Fatalf("%s answered %d: %v", name, status, res)
	}
	if msg, ok := res["rpcError"]; ok {
		return msg.(string), true
	}
	content, _ := res["content"].([]any)
	var text string
	for _, c := range content {
		text += c.(map[string]any)["text"].(string)
	}
	failed, _ := res["isError"].(bool)
	return text, failed
}

func mcpToolNames(t *testing.T, hub, token string) []string {
	t.Helper()
	_, res := mcpCall(t, hub, token, "tools/list", map[string]any{})
	var names []string
	for _, tool := range res["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

// TestAnotherAppOverRemoteMCP pairs apps at each access level and calls the
// machine through the hub's MCP endpoint, as Claude or ChatGPT would: what a
// level doesn't allow isn't there, a project a pairing doesn't cover doesn't
// exist for it, and a pairing the machine doesn't know — revoked, or one a
// hub made up — reaches nothing.
func TestAnotherAppOverRemoteMCP(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	leadReadyToChat(t, w.testDaemon, "hello-stack")

	if list, err := w.client.MCPPairings(ctx); err != nil || list.Available || list.Reason != "no-hub" {
		t.Fatalf("with no hub: %+v, %v", list, err)
	}
	if _, err := w.client.PairMCP(ctx, api.MCPPairRequest{Access: api.MCPAccessRead}); err == nil {
		t.Error("paired an app with no hub to reach the machine through")
	}
	h := hubtest.New(t)
	env := h.AddEnvironment("vps")
	if st, err := w.client.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: h.URL, Token: env.Token}); err != nil || !st.Connected {
		t.Fatalf("ConnectRemote = %+v, %v", st, err)
	}

	for _, bad := range []api.MCPPairRequest{
		{Access: "admin"},
		{Access: api.MCPAccessRead, Client: "netscape"},
		{Access: api.MCPAccessRead, Projects: []string{"no-such-project"}},
		{Access: api.MCPAccessRead, Projects: []string{"_home"}},
	} {
		if _, err := w.client.PairMCP(ctx, bad); err == nil {
			t.Errorf("paired %+v", bad)
		}
	}

	// Read only, one project.
	read, err := w.client.PairMCP(ctx, api.MCPPairRequest{Client: api.MCPClientClaude, Access: api.MCPAccessRead, Projects: []string{"hello-stack"}, Token: true})
	if err != nil {
		t.Fatal(err)
	}
	if read.Pairing.Label != "Claude" || read.Code == "" || read.Token == "" || read.URL != h.URL+hubapi.PathMCP {
		t.Errorf("the pairing = %+v", read)
	}
	status, res := mcpCall(t, h.URL, read.Token, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	if status != http.StatusOK || res["protocolVersion"] != "2025-06-18" || !strings.Contains(res["instructions"].(string), "read-only") {
		t.Errorf("initialize = %d %v", status, res)
	}
	if names := strings.Join(mcpToolNames(t, h.URL, read.Token), " "); names != "list_projects list_agents list_pull_requests read_lead_chat" {
		t.Errorf("a read-only pairing's tools = %s", names)
	}
	if text, failed := mcpTool(t, h.URL, read.Token, "list_projects", nil); failed || !strings.Contains(text, `"project": "hello-stack"`) || !strings.Contains(text, `"agents": 2`) {
		t.Errorf("list_projects = %q, %v", text, failed)
	}
	if text, failed := mcpTool(t, h.URL, read.Token, "list_agents", map[string]any{"project": "hello-stack"}); failed || !strings.Contains(text, "agent-01") || !strings.Contains(text, "Settings page") {
		t.Errorf("list_agents = %q, %v", text, failed)
	}
	if text, failed := mcpTool(t, h.URL, read.Token, "read_lead_chat", map[string]any{"project": "hello-stack"}); failed || !strings.Contains(text, "Nobody has written") {
		t.Errorf("read_lead_chat = %q, %v", text, failed)
	}
	if text, failed := mcpTool(t, h.URL, read.Token, "message_lead", map[string]any{"project": "hello-stack", "message": "Delete everything"}); !failed || !strings.Contains(text, "no tool named") {
		t.Errorf("a read-only pairing's message_lead = %q, %v", text, failed)
	}
	if w.tool.got("Delete everything") {
		t.Error("a read-only pairing wrote to the lead")
	}

	// Chat, every project. Another project the first pairing doesn't cover.
	other := w.fixtureRepo(t, "hello-android")
	if _, err := w.client.AddProject(ctx, api.AddProjectRequest{Path: other, Name: "other-stack"}); err != nil {
		t.Fatal(err)
	}
	if text, failed := mcpTool(t, h.URL, read.Token, "list_agents", map[string]any{"project": "other-stack"}); !failed || !strings.Contains(text, "no project named") {
		t.Errorf("a project the pairing doesn't cover = %q, %v", text, failed)
	}
	if text, _ := mcpTool(t, h.URL, read.Token, "list_projects", nil); strings.Contains(text, "other-stack") {
		t.Errorf("list_projects shows a project the pairing doesn't cover: %s", text)
	}
	chat, err := w.client.PairMCP(ctx, api.MCPPairRequest{Label: "Poke on my phone", Client: api.MCPClientPoke, Access: api.MCPAccessChat, Token: true})
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := mcpTool(t, h.URL, chat.Token, "list_projects", nil); !strings.Contains(text, "other-stack") || strings.Contains(text, "_home") {
		t.Errorf("list_projects for every project = %s", text)
	}
	text, failed := mcpTool(t, h.URL, chat.Token, "message_lead", map[string]any{"project": "hello-stack", "message": "How is the reminders page going?", "wait_seconds": 20})
	if failed || !strings.Contains(text, "The message was sent") {
		t.Errorf("message_lead = %q, %v", text, failed)
	}
	if !w.tool.got("How is the reminders page going?") || !w.tool.got("[Sent from Poke on my phone, another app connected to AgentBox over MCP]") {
		t.Error("the lead didn't get the message, saying where it came from")
	}
	if text, failed := mcpTool(t, h.URL, chat.Token, "read_lead_chat", map[string]any{"project": "hello-stack", "wait_seconds": 5}); failed || !strings.Contains(text, "How is the reminders page going?") {
		t.Errorf("read_lead_chat after a message = %q, %v", text, failed)
	}
	if names := strings.Join(mcpToolNames(t, h.URL, chat.Token), " "); !strings.Contains(names, "message_lead") || strings.Contains(names, "call_lead_tool") {
		t.Errorf("a chat pairing's tools = %s", names)
	}
	full, err := w.client.PairMCP(ctx, api.MCPPairRequest{Access: api.MCPAccessFull, Token: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(mcpToolNames(t, h.URL, full.Token), " "); !strings.HasSuffix(names, "message_lead lead_tools call_lead_tool") {
		t.Errorf("a full pairing's tools = %s", names)
	}

	list, err := w.client.MCPPairings(ctx)
	if err != nil || !list.Available || len(list.Pairings) != 3 || list.URL != h.URL+hubapi.PathMCP {
		t.Fatalf("pairings = %+v, %v", list, err)
	}
	if p := list.Pairings[0]; p.ID != read.Pairing.ID || p.Access != api.MCPAccessRead || len(p.Projects) != 1 || p.LastUsedAt.IsZero() {
		t.Errorf("the first pairing = %+v", p)
	}
	if again, err := w.client.RenewMCPCode(ctx, read.Pairing.ID); err != nil || again.Code == "" || again.Code == read.Code || again.Token != "" {
		t.Errorf("a new code = %+v, %v", again, err)
	}

	// The path exists for the hub's MCP endpoint only: not for a device
	// coming through the hub, whatever header it sends, nor on the socket.
	req, _ := http.NewRequest(http.MethodPost, h.URL+hubapi.EnvironmentAPI(env.Environment.ID)+hubapi.EnvironmentMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+h.Token())
	req.Header.Set(hubapi.HeaderMCPPairing, full.Pairing.ID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a device naming a pairing itself got %d", resp.StatusCode)
	}

	// A pairing the machine never made is nothing, even with a hub's word.
	_, forged := h.AddMCPPairing(env.Environment.ID)
	if status, _ := mcpCall(t, h.URL, forged, "tools/list", map[string]any{}); status != http.StatusForbidden {
		t.Errorf("a pairing the hub made up got %d", status)
	}
	// Listing tells the hub to drop what the machine doesn't know.
	if list, err := w.client.MCPPairings(ctx); err != nil || len(list.Pairings) != 3 {
		t.Errorf("pairings = %+v, %v", list, err)
	}
	if status, _ := mcpCall(t, h.URL, forged, "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("the made-up pairing after a listing got %d", status)
	}

	// Revoking ends it on the machine at once, and on the hub.
	if err := w.client.RevokeMCPPairing(ctx, chat.Pairing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.store.MCPPairing(ctx, h.URL, chat.Pairing.ID); err == nil {
		t.Error("the revoked pairing is still in state.db")
	}
	if status, _ := mcpCall(t, h.URL, chat.Token, "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("a revoked pairing got %d", status)
	}
	if err := w.client.RevokeMCPPairing(ctx, chat.Pairing.ID); !api.IsNotFound(err) {
		t.Errorf("revoking twice: %v", err)
	}

	// Leaving the hub takes the rest with it.
	if err := w.client.DisconnectRemote(ctx); err != nil {
		t.Fatal(err)
	}
	if left, _ := w.srv.store.MCPPairings(ctx, h.URL); len(left) != 0 {
		t.Errorf("pairings after leaving the hub: %+v", left)
	}
	if h.MCPToken(read.Pairing.ID) != "" {
		t.Error("the hub still has a pairing of a machine that left")
	}
}
