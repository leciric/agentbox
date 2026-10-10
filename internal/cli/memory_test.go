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

	"agentbox/internal/api"
)

// TestRequestTools covers request_credential, request_connector and
// record_artifact against the fake agent API: how an agent asks for a
// credential or a connector it lacks, and records what it made.
func TestRequestTools(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent2.sock")
	calls := make(chan string, 8)
	serveFakeAgentAPI(t, socket, calls)
	tools := agentMemoryTools(context.Background(), api.NewClient(socket))
	byName := map[string]int{}
	for i, tool := range tools {
		byName[tool.Name] = i
	}

	if _, ok := byName["my_task"]; ok {
		t.Error("an agent still has my_task: there is no task list to read")
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
	for _, want := range []string{"search_memory", "report", "record_artifact"} {
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
		switch r.URL.Path {
		case "/v1/self/memory/reports":
			_ = json.NewEncoder(w).Encode(api.AgentReport{ID: "rep_test", Status: "partial", RemainingIssues: []string{"Pagination"}})
		case "/v1/self/memory/artifacts":
			_ = json.NewEncoder(w).Encode(api.MemoryArtifact{ID: "art_test", Path: "x"})
		case "/v1/self":
			_ = json.NewEncoder(w).Encode(api.Self{Ref: "pawly/agent-01", Agent: "agent-01", Project: "pawly"})
		case "/v1/self/credential":
			_ = json.NewEncoder(w).Encode(api.Question{ID: "q1", Answer: "use work", Status: "answered"})
		case "/v1/self/connector":
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
