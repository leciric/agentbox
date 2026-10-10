package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// TestMyTaskTools covers my_task, request_credential and request_connector
// against the fake agent API: what an agent reads about its own work, that it
// has nothing to change the user's task list with, and how it asks for a
// credential or a connector it lacks.
func TestMyTaskTools(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent2.sock")
	calls := make(chan string, 8)
	serveFakeAgentAPI(t, socket, calls)
	tools := agentMemoryTools(context.Background(), api.NewClient(socket))
	byName := map[string]int{}
	for i, tool := range tools {
		byName[tool.Name] = i
	}

	out, err := tools[byName["my_task"]].Run(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Fix login") || !strings.Contains(out, "task_1") {
		t.Errorf("my_task = %q", out)
	}
	<-calls // /v1/self
	<-calls // /v1/self/memory/tasks

	if _, ok := byName["update_my_task"]; ok {
		t.Error("an agent has update_my_task: the task list is the user's to change")
	}

	if _, ok := byName["request_credential"]; !ok {
		t.Fatal("no request_credential tool")
	}
	ans, err := tools[byName["request_credential"]].Wait(context.Background(), json.RawMessage(`{"kind":"secret","name":"STRIPE_KEY","reason":"needs it for the checkout test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ans != "use work" {
		t.Errorf("request_credential = %q, want the answer text", ans)
	}

	ans, err = tools[byName["request_connector"]].Wait(context.Background(),
		json.RawMessage(`{"name":"notion","url":"https://mcp.notion.com/mcp","reason":"the spec is in Notion"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ans != "The user connected notion (https://mcp.notion.com/mcp) for the spec is in Notion" {
		t.Errorf("request_connector = %q, want the answer text", ans)
	}

	art, err := tools[byName["record_artifact"]].Run(json.RawMessage(`{"type":"branch","path":"agentbox/agent-01"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art, "art_test") {
		t.Errorf("record_artifact = %q", art)
	}
}

// The memory MCP server is the agent's, like the desktop one: on the host
// there is no agent it would be about, and it says so rather than failing
// later with a socket error.
func TestMemoryMCPNeedsAnAgent(t *testing.T) {
	if _, err := os.Stat(api.InAgentSocket); err == nil {
		t.Skip("running inside an agent")
	}
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "nothing.sock"))
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"memory", "mcp"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "runs inside an agent") {
		t.Errorf("agentbox memory mcp outside an agent: got %v", err)
	}
}

// What a worker agent's tools are, and what they aren't: it reads the whole of
// its project's memory and adds to the record of what it did, but curating
// what the project remembers is the project chat's.
func TestAgentMemoryTools(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	calls := make(chan string, 8)
	serveFakeAgentAPI(t, socket, calls)
	tools := agentMemoryTools(context.Background(), api.NewClient(socket))

	byName := map[string]int{}
	for i, tool := range tools {
		byName[tool.Name] = i
		if tool.Description == "" {
			t.Errorf("%s has no description: it is all a model reads", tool.Name)
		}
	}
	for _, want := range []string{"search_memory", "memory_feedback", "report", "record_artifact"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("a worker agent has no %s tool", want)
		}
	}
	for _, unwanted := range []string{"remember", "update_working_memory"} {
		if _, ok := byName[unwanted]; ok {
			t.Errorf("a worker agent has %s: curating memory is the project chat's", unwanted)
		}
	}

	// A report goes to the agent's own routes, which are what decide who it
	// came from, and the answer names the id the chat will see it under.
	out, err := tools[byName["report"]].Run(json.RawMessage(`{"summary":"It works.","status":"partial","remaining_issues":["Pagination"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rep_test") || !strings.Contains(out, "1 thing(s) left undone") {
		t.Errorf("the report's answer = %q", out)
	}
	if got := <-calls; got != "POST /v1/self/memory/reports" {
		t.Errorf("report called %s", got)
	}

	// Feedback goes to the agent's own routes too, and says what it did.
	out, err = tools[byName["memory_feedback"]].Run(json.RawMessage(`{"memory":"The API listens on 7777","verdict":"wrong","why":"8080"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != `Noted mem_1 ("The API listens on 7777") as wrong: importance 1, was 5.` {
		t.Errorf("memory_feedback = %q", out)
	}
	if got := <-calls; got != "POST /v1/self/memory/feedback" {
		t.Errorf("memory_feedback called %s", got)
	}

	// A report with nothing in it is refused here, before the round trip.
	if _, err := tools[byName["report"]].Run(json.RawMessage(`{"summary":"   "}`)); err == nil {
		t.Error("a report with no summary was accepted")
	}
}

// TestWhoamiInsideAnAgent covers whoami's happy path, against the fake agent
// API, which also answers /v1/self.
func TestWhoamiInsideAnAgent(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "whoami.sock")
	calls := make(chan string, 8)
	serveFakeAgentAPI(t, socket, calls)
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", socket)

	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"whoami"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "pawly/agent-01") || !strings.Contains(got, "agent-01") {
		t.Errorf("whoami = %q", got)
	}
}

// serveFakeAgentAPI answers the in-agent memory routes, so the tools can be
// exercised without a daemon.
func serveFakeAgentAPI(t *testing.T, socket string, calls chan<- string) {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/self/memory/reports":
			_ = json.NewEncoder(w).Encode(api.AgentReport{ID: "rep_test", Status: "partial", RemainingIssues: []string{"Pagination"}})
		case r.URL.Path == "/v1/self/memory/artifacts":
			_ = json.NewEncoder(w).Encode(api.MemoryArtifact{ID: "art_test", Path: "x"})
		case r.URL.Path == "/v1/self":
			_ = json.NewEncoder(w).Encode(api.Self{Ref: "pawly/agent-01", Agent: "agent-01", Project: "pawly"})
		case r.URL.Path == "/v1/self/memory/tasks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.Task{{ID: "task_1", Agent: "agent-01", Status: "active", Goal: "Fix login"}})
		case strings.HasPrefix(r.URL.Path, "/v1/self/memory/tasks/") && r.Method == http.MethodPatch:
			var in api.UpdateTaskRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			out := api.Task{ID: "task_1", Agent: "agent-01", Goal: "Fix login", Status: "active"}
			if in.Status != nil {
				out.Status = *in.Status
			}
			if in.Detail != nil {
				out.Detail = *in.Detail
			}
			_ = json.NewEncoder(w).Encode(out)
		case r.URL.Path == "/v1/self/memory/feedback":
			var in api.MemoryFeedbackRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(api.MemoryFeedbackResult{Was: 5,
				Memory: api.Memory{ID: "mem_1", Title: in.Memory, Importance: 1}})
		case r.URL.Path == "/v1/self/credential":
			_ = json.NewEncoder(w).Encode(api.Question{ID: "q1", Answer: "use work", Status: "answered"})
		case r.URL.Path == "/v1/self/connector":
			var in api.ConnectorRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(api.Question{ID: "q2", Kind: api.QuestionConnector, Connector: in.Name,
				Answer: "The user connected " + in.Name + " (" + in.URL + ") for " + in.Reason, Status: "answered"})
		default:
			_ = json.NewEncoder(w).Encode(api.MemorySearchResults{})
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "36h": 36 * time.Hour} {
		got, err := parseAge(in)
		if err != nil || got != want {
			t.Errorf("parseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "soon", "10m", "-3d"} {
		if _, err := parseAge(in); err == nil {
			t.Errorf("parseAge(%q) should fail", in)
		}
	}
}

func TestDescribeTidy(t *testing.T) {
	now := time.Now()
	plan := api.TidyMemoryResult{
		Resolved: []api.Memory{{ID: "mem_1", Kind: "issue", Title: "Stale --model help text", CreatedAt: now.Add(-12 * 24 * time.Hour)}},
		Merged: []api.MemoryMerge{{Memory: api.Memory{ID: "mem_2", Title: "Agent-name reuse bug unfixed"},
			Into: api.Memory{ID: "mem_3", Title: "Agent names are reused as soon as an agent is gone"}, Score: 0.48, Why: "says the same thing"}},
		Kept: 4,
	}
	out := describeTidy("agentbox", "7d", plan, now)
	for _, want := range []string{
		"agentbox: would resolve 1 open item nobody has mentioned in 7d, and would merge 1 duplicate. 4 stay open.",
		"mem_1  [issue] Stale --model help text (12 days old)",
		"→ mem_3  Agent names are reused as soon as an agent is gone (says the same thing, 0.48)",
		"--apply",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	plan.Applied = true
	if out := describeTidy("agentbox", "7d", plan, now); strings.Contains(out, "--apply") || !strings.Contains(out, "resolved 1") {
		t.Errorf("applied:\n%s", out)
	}
}
