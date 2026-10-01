package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// The Home chat's tools name the project they act on, and reach it at the
// user's own paths on the Home socket.
func TestHomeTools(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "home.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var told, searched []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.Project{{Name: "pawly", Root: "/src/pawly", Branch: "main"}, {Name: "shop", Root: "/src/shop", Branch: "main"}})
	})
	mux.HandleFunc("GET /v1/projects/{project}/fleet", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Fleet{Agents: []api.FleetAgent{{Agent: api.Agent{Name: "agent-01", State: "running"}}}})
	})
	mux.HandleFunc("POST /v1/projects/{project}/lead/messages", func(w http.ResponseWriter, r *http.Request) {
		var req api.ChatMessageRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		told = append(told, r.PathValue("project")+": "+req.Text)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc("POST /v1/projects/{project}/memory/search", func(w http.ResponseWriter, r *http.Request) {
		searched = append(searched, r.PathValue("project"))
		results := api.MemorySearchResults{}
		if r.PathValue("project") == "shop" {
			results.Memories = []api.Memory{{ID: "m1", Title: "Checkout needs Redis on 6379", Kind: "gotcha"}}
		}
		_ = json.NewEncoder(w).Encode(results)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	byName := map[string]mcp.Tool{}
	for _, tool := range homeTools(context.Background(), api.NewClient(socket)) {
		byName[tool.Name] = tool
	}
	for _, want := range []string{"list_projects", "list_agents", "read_agent", "create_agent", "tell_lead", "search_memory", "add_project"} {
		if tool, ok := byName[want]; !ok || tool.Description == "" {
			t.Errorf("the Home chat has no %s tool, or it isn't described", want)
		}
	}
	run := func(name, args string) string {
		t.Helper()
		out, err := byName[name].Run(json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s(%s) = %v", name, args, err)
		}
		return out
	}
	if out := run("list_projects", ""); !strings.Contains(out, "pawly: /src/pawly") || !strings.Contains(out, "1 running") {
		t.Errorf("list_projects = %q", out)
	}
	run("tell_lead", `{"project":"pawly","message":"add reminders"}`)
	if len(told) != 1 || told[0] != "pawly: add reminders" {
		t.Errorf("tell_lead told %v", told)
	}
	// Without a project, every project is searched, and only what found
	// something is shown.
	out := run("search_memory", `{"query":"redis"}`)
	if len(searched) != 2 || !strings.Contains(out, "## shop") || strings.Contains(out, "## pawly") {
		t.Errorf("search_memory searched %v and said %q", searched, out)
	}
	if _, err := byName["create_agent"].Run(json.RawMessage(`{"title":"x","task":"y"}`)); err == nil {
		t.Error("create_agent made an agent without naming a project")
	}
}
