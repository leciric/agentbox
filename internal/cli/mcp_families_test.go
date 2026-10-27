package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// leadToolsAgainst is projectTools against a lead socket that answers every
// route with {} and records what was called, in order.
func leadToolsAgainst(t *testing.T, settings api.Settings) (map[string]mcp.Tool, func() []string) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "lead.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var hits []string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/project/settings", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(settings) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		hit := r.Method + " " + r.URL.Path
		if paused, ok := body["paused"]; ok {
			hit += " paused=" + map[bool]string{true: "true", false: "false"}[paused == true]
		}
		mu.Lock()
		hits = append(hits, hit)
		mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	byName := map[string]mcp.Tool{}
	for _, tool := range projectTools(context.Background(), api.NewClient(socket)) {
		byName[tool.Name] = tool
	}
	return byName, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := hits
		hits = nil
		return out
	}
}

// TestLeadToolsDoWhatTheOldOnesDid: the lead's 52 tools became 21 (one tool
// a family, with an action), and each old tool's call has a new one that
// reaches the same route of the lead socket. This table is the mapping.
func TestLeadToolsDoWhatTheOldOnesDid(t *testing.T) {
	tools, hits := leadToolsAgainst(t, api.Settings{})
	hits()
	for _, tc := range []struct{ old, tool, args, route string }{
		{"list_agents", "list_agents", `{}`, "GET /v1/project/agents"},
		{"create_agent", "create_agent", `{"title":"t","task":"t"}`, "POST /v1/project/agents"},
		{"fan_out", "fan_out", `{"title":"t","task":"t","variants":[{},{}]}`, "POST /v1/project/groups"},
		{"compare_group", "compare_group", `{"group":"g"}`, "GET /v1/project/groups/g"},
		{"recommend_agent", "compare_group", `{"group":"g","recommend":"agent-01","why":"w"}`, "POST /v1/project/groups/g/recommend"},
		{"tell_agent", "tell_agent", `{"agent":"agent-01","message":"m"}`, "POST /v1/project/agents/agent-01/chat/messages"},
		{"read_agent", "read_agent", `{"agent":"agent-01"}`, "GET /v1/project/agents/agent-01/chat"},
		{"agent_diff", "read_agent", `{"agent":"agent-01","show":"diff"}`, "GET /v1/project/agents/agent-01/diff"},
		{"agent_diff stat", "read_agent", `{"agent":"agent-01","show":"stat"}`, "GET /v1/project/agents/agent-01/diff"},
		{"run_in_agent", "run_in_agent", `{"agent":"agent-01","command":"ls"}`, "POST /v1/project/agents/agent-01/run"},
		{"copy_between_agents", "copy_to_agent", `{"from":"agent-02","path":"p","to":"agent-01"}`, "POST /v1/project/copy"},
		{"copy_to_agent", "copy_to_agent", `{"path":"/p","to":"agent-01"}`, "POST /v1/project/copy-from-host"},
		{"retire_agent", "retire_agent", `{"agent":"agent-01"}`, "POST /v1/project/retire"},
		{"agent_checkpoints", "checkpoints", `{"action":"list","agent":"agent-01"}`, "GET /v1/project/agents/agent-01/checkpoints"},
		{"roll_back_agent", "checkpoints", `{"action":"roll_back","agent":"agent-01","checkpoint":"turn-1"}`, "POST /v1/project/agents/agent-01/rollback"},
		{"fork_agent", "checkpoints", `{"action":"fork","agent":"agent-01"}`, "POST /v1/project/agents/agent-01/fork"},
		{"read_notes", "notes", `{"action":"read"}`, "GET /v1/project/notes"},
		{"append_note", "notes", `{"action":"append","text":"t"}`, "POST /v1/project/notes"},
		{"edit_note", "notes", `{"action":"edit","match":"m","text":"t"}`, "POST /v1/project/notes/edit"},
		{"remove_note", "notes", `{"action":"remove","match":"m"}`, "POST /v1/project/notes/remove"},
		{"promote_memory", "notes", `{"action":"promote","id":"x"}`, "POST /v1/project/memory/promote"},
		{"dismiss_promotion", "notes", `{"action":"dismiss","id":"x"}`, "POST /v1/project/memory/dismiss-promotion"},
		{"search_memory", "search_memory", `{"query":"q"}`, "POST /v1/project/memory/search"},
		{"remember", "remember", `{"title":"t"}`, "POST /v1/project/memory/memories"},
		{"memory_feedback", "memory_feedback", `{"memory":"x","verdict":"stale","why":"w"}`, "POST /v1/project/memory/feedback"},
		{"resolve_memory", "memory_feedback", `{"memory":"x","verdict":"resolved","why":"w"}`, "POST /v1/project/memory/resolve"},
		{"project_state", "project_state", `{}`, "GET /v1/project/memory/working"},
		{"update_working_memory", "project_state", `{"goal":"g"}`, "PATCH /v1/project/memory/working"},
		{"list_skills", "skills", `{"action":"list"}`, "GET /v1/project/skills"},
		{"read_skill", "skills", `{"action":"read","name":"s"}`, "GET /v1/project/skills/s"},
		{"create_skill", "skills", `{"action":"create","name":"s","content":"c"}`, "POST /v1/project/skills"},
		{"edit_skill", "skills", `{"action":"edit","name":"s","content":"c"}`, "PUT /v1/project/skills/s"},
		{"switch_skill", "skills", `{"action":"switch","name":"s","state":"on"}`, "PUT /v1/project/skills/s/switch"},
		{"delete_skill", "skills", `{"action":"delete","name":"s"}`, "DELETE /v1/project/skills/s"},
		{"message_project", "message_project", `{"action":"send","project":"p","message":"m"}`, "POST /v1/project/threads"},
		{"align_with_project", "message_project", `{"action":"align","project":"p","topic":"t","message":"m"}`, "POST /v1/project/threads"},
		{"reply_project", "message_project", `{"action":"reply","thread_id":"th","message":"m"}`, "POST /v1/project/threads/th/replies"},
		{"settle_alignment", "message_project", `{"action":"settle","thread_id":"th","summary":"s"}`, "POST /v1/project/threads/th/settle"},
		{"list_project_threads", "message_project", `{"action":"threads"}`, "GET /v1/project/threads"},
		{"list_questions", "questions", `{"action":"list"}`, "GET /v1/project/questions"},
		{"answer_question", "questions", `{"action":"answer","id":"x","text":"a"}`, "POST /v1/project/questions/x/answer"},
		{"escalate_question", "questions", `{"action":"escalate","id":"x","text":"w"}`, "POST /v1/project/questions/x/escalate"},
		{"list_accounts", "list_resources", `{"kind":"accounts"}`, "GET /v1/project/accounts"},
		{"list_secrets", "list_resources", `{"kind":"secrets"}`, "GET /v1/project/secrets"},
		{"list_connectors", "list_resources", `{"kind":"connectors"}`, "GET /v1/project/connectors"},
		{"list_issues", "list_resources", `{"kind":"issues"}`, "GET /v1/project/issues"},
		{"switch_agent_account", "switch_agent_account", `{"agent":"agent-01","account":"work"}`, "POST /v1/project/agents/agent-01/account"},
		{"fulfil_credential", "questions", `{"action":"fulfil","id":"x"}`, "POST /v1/project/questions/x/fulfil"},
		{"schedule_task", "schedules", `{"action":"create","task":"t","in":"2h"}`, "POST /v1/project/schedules"},
		{"list_schedules", "schedules", `{"action":"list"}`, "GET /v1/project/schedules"},
		{"edit_schedule", "schedules", `{"action":"edit","id":"x","task":"t"}`, "PATCH /v1/project/schedules/x"},
		{"pause_schedule", "schedules", `{"action":"pause","id":"x"}`, "PATCH /v1/project/schedules/x paused=true"},
		{"pause_schedule paused false", "schedules", `{"action":"resume","id":"x"}`, "PATCH /v1/project/schedules/x paused=false"},
		{"delete_schedule", "schedules", `{"action":"delete","id":"x"}`, "DELETE /v1/project/schedules/x"},
	} {
		tool, ok := tools[tc.tool]
		if !ok {
			t.Errorf("%s: there is no %s tool", tc.old, tc.tool)
			continue
		}
		_, _ = tool.Run(json.RawMessage(tc.args)) // {} is no answer to most; the route is what is under test
		if got := hits(); len(got) == 0 || got[0] != tc.route {
			t.Errorf("%s, now %s(%s), called %v, want %s", tc.old, tc.tool, tc.args, got, tc.route)
		}
	}
	if len(tools) != 21 {
		t.Errorf("the lead has %d tools, want 21", len(tools))
	}
}

// TestLeadToolsBudget holds the lead's tool list, which its model is sent
// with every call it makes, to about its size when it was cut from 49 tools
// to 20 (16.1k tokens to 8.7k). A tool or a description may grow, but past
// this, look for what to take out first. It is measured with every optional
// tool and menu on, the longest the list gets.
func TestLeadToolsBudget(t *testing.T) {
	settings := api.Settings{OpenCodeReady: true, CursorReady: true, Timezone: "America/Sao_Paulo"}
	for _, m := range []string{"default", "opus", "sonnet", "haiku", "fable"} {
		settings.ClaudeModelChoices = append(settings.ClaudeModelChoices, api.ChatOptionChoice{Value: m})
	}
	for _, e := range []string{"low", "medium", "high", "xhigh", "max"} {
		settings.ClaudeEffortChoices = append(settings.ClaudeEffortChoices, api.ChatOptionChoice{Value: e})
	}
	settings.OpenCodeModelChoices = []api.ChatOptionChoice{{Value: "anthropic/claude-sonnet-5-5"}, {Value: "openai/gpt-6"}}
	settings.CursorModelChoices = []api.ChatOptionChoice{{Value: "default"}, {Value: "composer-2", Efforts: []string{"low", "high"}}}
	tools, _ := leadToolsAgainst(t, settings)
	var list []map[string]any
	for _, tool := range tools {
		list = append(list, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.Schema})
	}
	raw, _ := json.Marshal(list)
	const budget = 26 << 10
	if len(raw) > budget {
		t.Errorf("the lead's tools are %d bytes, over the %d budget", len(raw), budget)
	}
	for name, tool := range tools {
		if tool.Description == "" || strings.Contains(tool.Description, "  ") {
			t.Errorf("%s's description is empty or has a double space: %q", name, tool.Description)
		}
	}
}
