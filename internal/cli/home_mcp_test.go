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
	var told, searched, goals []string
	var created api.CreateAgentRequest
	var remembered api.AddMemoryRequest
	pref := api.Memory{ID: "m9", Title: "Agent preference: Redis via Docker", Kind: "decision", Global: true, Project: "*"}
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
		// Every project's search returns what is remembered for all of them.
		results.Memories = []api.Memory{pref}
		if r.PathValue("project") == "shop" {
			results.Memories = append(results.Memories, api.Memory{ID: "m1", Title: "Checkout needs Redis on 6379", Kind: "gotcha"})
		}
		_ = json.NewEncoder(w).Encode(results)
	})
	mux.HandleFunc("POST /v1/global/memory/search", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.MemorySearchResults{Memories: []api.Memory{pref}})
	})
	mux.HandleFunc("POST /v1/global/memory/memories", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&remembered)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Memory{ID: "m10", Title: remembered.Title, Global: true})
	})
	mux.HandleFunc("POST /v1/agents", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.Job{ID: "j1"})
	})
	mux.HandleFunc("PUT /v1/agents/{project}/{agent}/goal", func(w http.ResponseWriter, r *http.Request) {
		var spec api.GoalSpec
		_ = json.NewDecoder(r.Body).Decode(&spec)
		goals = append(goals, r.PathValue("project")+"/"+r.PathValue("agent")+": "+spec.Objective)
		_ = json.NewEncoder(w).Encode(api.GoalResult{Goal: &api.AgentGoal{Objective: spec.Objective, Status: api.GoalActive, Budget: 10}, Woke: "started"})
	})
	mux.HandleFunc("DELETE /v1/agents/{project}/{agent}/goal", func(w http.ResponseWriter, r *http.Request) {
		goals = append(goals, r.PathValue("project")+"/"+r.PathValue("agent")+": cleared")
		_ = json.NewEncoder(w).Encode(api.GoalResult{Goal: &api.AgentGoal{Objective: "x", Status: api.GoalCleared}})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	byName := map[string]mcp.Tool{}
	for _, tool := range homeTools(context.Background(), api.NewClient(socket)) {
		byName[tool.Name] = tool
	}
	for _, want := range []string{"list_projects", "list_agents", "read_agent", "create_agent", "tell_lead", "search_memory",
		"remember", "resolve_memory", "add_project", "set_goal"} {
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
	// something is shown; what holds in all of them comes first, once.
	out := run("search_memory", `{"query":"redis"}`)
	if len(searched) != 2 || !strings.Contains(out, "## shop") || strings.Contains(out, "## pawly") {
		t.Errorf("search_memory searched %v and said %q", searched, out)
	}
	if !strings.HasPrefix(out, "## All projects") || strings.Count(out, "[m9]") != 1 || !strings.Contains(out, "all projects)") {
		t.Errorf("search_memory showed the AgentBox-wide memory as %q", out)
	}
	if out := run("remember", `{"title":"Agent preference: one agent at a time","kind":"decision"}`); !strings.Contains(out, "every project") {
		t.Errorf("remember said %q", out)
	}
	if remembered.Title != "Agent preference: one agent at a time" || remembered.Kind != "decision" {
		t.Errorf("remember sent %+v", remembered)
	}
	if _, err := byName["create_agent"].Run(json.RawMessage(`{"title":"x","task":"y"}`)); err == nil {
		t.Error("create_agent made an agent without naming a project")
	}
	run("create_agent", `{"project":"pawly","title":"x","task":"y","goal":"a PR is open","goal_checks":["pr_open","go test ./..."],"goal_budget":3}`)
	if g := created.Goal; g == nil || g.Objective != "a PR is open" || g.Budget != 3 || len(g.Checks) != 2 || g.Checks[1].Command != "go test ./..." {
		t.Errorf("create_agent sent goal %+v", created.Goal)
	}
	if out := run("set_goal", `{"project":"pawly","agent":"agent-01","goal":"CI is green","goal_checks":["ci_green"]}`); !strings.Contains(out, "started its machine") {
		t.Errorf("set_goal said %q", out)
	}
	if out := run("set_goal", `{"project":"pawly","agent":"agent-01"}`); !strings.Contains(out, "Cleared") {
		t.Errorf("set_goal with no goal said %q", out)
	}
	if strings.Join(goals, "; ") != "pawly/agent-01: CI is green; pawly/agent-01: cleared" {
		t.Errorf("set_goal sent %v", goals)
	}
	if _, err := byName["set_goal"].Run(json.RawMessage(`{"project":"pawly","agent":"agent-01","goal_budget":3}`)); err == nil {
		t.Error("set_goal took a budget with no goal")
	}
}
