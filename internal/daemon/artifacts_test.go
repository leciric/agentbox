package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeHatch is Hatch's MCP server, as far as get_page goes, and its content
// origin, which serves a page's HTML to whoever has the link get_page gave.
type fakeHatch struct {
	*httptest.Server
	mu sync.Mutex
	// contentURL is whether get_page answers with content_url, as Hatch does
	// since leciric/hatch#3.
	contentURL bool
	seen       []string // the Authorization of each MCP request
	gone       map[string]bool
}

func newFakeHatch(t *testing.T) *fakeHatch {
	f := &fakeHatch{contentURL: true, gone: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", f.mcp)
	mux.HandleFunc("GET /a/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "signed-"+r.PathValue("id") || r.Header.Get("Authorization") != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; frame-ancestors https://hatch.example; sandbox allow-scripts")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Page " + r.PathValue("id") + "</h1>"))
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeHatch) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&msg)
	f.mu.Lock()
	f.seen = append(f.seen, r.Header.Get("Authorization"))
	withContent, gone := f.contentURL, f.gone[msg.Params.Arguments["id"]]
	f.mu.Unlock()
	answer := func(result any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
	switch {
	case msg.Method == "initialize":
		w.Header().Set("Mcp-Session-Id", "s1")
		answer(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "hatch"}})
	case len(msg.ID) == 0:
		w.WriteHeader(http.StatusAccepted)
	case msg.Method == "tools/call" && msg.Params.Name == "get_page" && gone:
		answer(map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Error (not_found): Artifact not found."}}})
	case msg.Method == "tools/call" && msg.Params.Name == "get_page":
		id := msg.Params.Arguments["id"]
		page := map[string]any{"artifact": map[string]any{
			"id": id, "title": "Q3 report (final)", "visibility": "private", "current_version": 4, "status": "active",
			"expires_at": "2099-01-01T00:00:00.000Z", "url": "https://hatch.linting.dev/p/" + id,
		}, "versions": []any{}}
		if withContent {
			page["content_url"] = f.URL + "/a/" + id + "?t=signed-" + id
		}
		answer(map[string]any{"content": []any{map[string]any{"type": "text", "text": "Q3 report"}}, "structuredContent": page})
	default:
		answer(map[string]any{})
	}
}

// A project's artifacts, through the daemon: read out of its lead's and its
// agents' chats when Hatch is on for the project, refreshed and shown
// through the connector, and nothing at all when it is off.
func TestArtifacts(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	hatch := newFakeHatch(t)
	u, _ := url.Parse(hatch.URL)
	d.srv.hatchHost = u.Host
	link := "http://" + u.Host + "/p/"

	now := time.Now().UTC()
	item := func(id string, at time.Time, tool api.ChatTool) state.ChatItem {
		data, _ := json.Marshal(api.ChatItem{ID: id, Kind: "tool", Tool: &tool, CreatedAt: at, UpdatedAt: at})
		return state.ChatItem{ID: id, Position: at.UnixNano(), Data: data}
	}
	if err := d.srv.store.SaveChatItems(ctx, "hello-stack", "lead", []state.ChatItem{
		item("l1", now.Add(-2*time.Hour), api.ChatTool{Name: "mcp__hatch__publish_page", Status: "completed", Page: &api.ChatPage{Title: "Q3 report"},
			Output: `Published "Q3 report" (id Q3, version 1): ` + link + "Q3"}),
		item("l2", now.Add(-30*time.Hour), api.ChatTool{Name: "mcp__hatch__publish_page", Status: "completed", Page: &api.ChatPage{Title: "Old"},
			Output: `Published "Old" (id Old, version 1): ` + link + "Old"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SaveChatItems(ctx, "hello-stack", "agent-01", []state.ChatItem{
		item("a1", now.Add(-time.Hour), api.ChatTool{Name: "mcp__hatch__update_page", Status: "completed", Page: &api.ChatPage{ID: "Q3"},
			Output: "Published version 2 of Q3. Share link: " + link + "Q3"}),
		item("a2", now.Add(-time.Hour), api.ChatTool{Name: "mcp__hatch__list_pages", Status: "completed", Output: link + "Theirs"}),
	}); err != nil {
		t.Fatal(err)
	}

	// No Hatch connector: nothing, however many pages the chats made.
	list, err := d.client.Artifacts(ctx, "hello-stack", "")
	if err != nil || list.Connector != "" || len(list.Artifacts) != 0 {
		t.Fatalf("without Hatch: %+v, %v", list, err)
	}

	// An AgentBox-wide one, signed in with a token, is the project's too.
	if _, err := d.client.SetConnector(ctx, "", "hatch", api.SetConnectorRequest{URL: hatch.URL + "/mcp", Secret: "HATCH_TOKEN", SecretValue: "shr_1"}); err != nil {
		t.Fatal(err)
	}
	list, err = d.client.Artifacts(ctx, "hello-stack", "")
	if err != nil || list.Connector != "hatch" || len(list.Artifacts) != 2 {
		t.Fatalf("with Hatch: %+v, %v", list, err)
	}
	q3, old := list.Artifacts[0], list.Artifacts[1]
	if q3.ID != "Q3" || q3.Title != "Q3 report" || q3.Agent != "hello-stack/lead" || q3.Item != "l1" || q3.Version != 2 ||
		strings.Join(q3.Agents, ",") != "hello-stack/lead,hello-stack/agent-01" || q3.Expired || q3.ExpiresAt == nil {
		t.Errorf("Q3 = %+v", q3)
	}
	if old.ID != "Old" || !old.Expired {
		t.Errorf("Old = %+v", old)
	}
	mine, err := d.client.Artifacts(ctx, "hello-stack", "agent-01")
	if err != nil || len(mine.Artifacts) != 1 || mine.Artifacts[0].ID != "Q3" {
		t.Errorf("agent-01's: %+v, %v", mine, err)
	}

	// Opening one asks Hatch, with the connector's token, and serves its
	// HTML with Hatch's policy minus frame-ancestors.
	preview, err := d.client.ArtifactPreview(ctx, "hello-stack", "Q3")
	if err != nil || preview.Status != api.ArtifactActive || !preview.Page || preview.Artifact.Title != "Q3 report (final)" ||
		preview.Artifact.Version != 4 || preview.Artifact.ExpiresAt == nil || preview.Artifact.ExpiresAt.Year() != 2099 {
		t.Errorf("preview = %+v, %v", preview, err)
	}
	hatch.mu.Lock()
	if len(hatch.seen) == 0 || hatch.seen[len(hatch.seen)-1] != "Bearer shr_1" {
		t.Errorf("Hatch saw %v", hatch.seen)
	}
	hatch.mu.Unlock()
	resp, err := d.client.HTTPClient().Get("http://agentbox/v1/projects/hello-stack/artifacts/Q3/page")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if resp.StatusCode != http.StatusOK || string(body[:n]) != "<h1>Page Q3</h1>" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") ||
		strings.Contains(csp, "frame-ancestors") || !strings.Contains(csp, "sandbox allow-scripts") {
		t.Errorf("page: %d %q %v", resp.StatusCode, body[:n], resp.Header)
	}

	// A page no chat of the project made isn't Hatch's to show here.
	if _, err := d.client.ArtifactPreview(ctx, "hello-stack", "Theirs"); err == nil {
		t.Error("previewed a page the project didn't make")
	}

	// A Hatch from before content_url: the details, no page.
	hatch.mu.Lock()
	hatch.contentURL = false
	hatch.gone["Old"] = true
	hatch.mu.Unlock()
	d.srv.pageLinks.set("hello-stack/Q3", "")
	if preview, err := d.client.ArtifactPreview(ctx, "hello-stack", "Q3"); err != nil || preview.Page || preview.Error == "" || preview.Status != api.ArtifactActive {
		t.Errorf("without content_url: %+v, %v", preview, err)
	}
	if _, err := d.client.ArtifactPage(ctx, "hello-stack", "Q3"); err == nil {
		t.Error("served a page Hatch gave no HTML for")
	}
	if preview, err := d.client.ArtifactPreview(ctx, "hello-stack", "Old"); err != nil || preview.Status != api.ArtifactExpired || !preview.Artifact.Expired {
		t.Errorf("gone: %+v, %v", preview, err)
	}

	// Turned off for the project: nothing again.
	if _, err := d.client.SetConnectorOverride(ctx, "hello-stack", "hatch", "off"); err != nil {
		t.Fatal(err)
	}
	if list, err := d.client.Artifacts(ctx, "hello-stack", ""); err != nil || list.Connector != "" || len(list.Artifacts) != 0 {
		t.Errorf("turned off: %+v, %v", list, err)
	}
}
